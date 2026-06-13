package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/pauluszhou/bubbles/internal/ipc"
	"github.com/pauluszhou/bubbles/internal/model"
	"github.com/pauluszhou/bubbles/internal/store"
)

// Scheduler manages cron jobs and one-time tasks.
type Scheduler struct {
	cron     *cron.Cron
	store    *store.Store
	executor *Executor
	ipc      *ipc.Server
	started  time.Time
	entryMap map[string]cron.EntryID // taskID -> cron entry ID
}

func NewScheduler(s *store.Store) *Scheduler {
	return &Scheduler{
		cron:     cron.New(), // 标准 5 段 cron
		store:    s,
		executor: NewExecutor(s),
		entryMap: make(map[string]cron.EntryID),
	}
}

// Run starts the scheduler and IPC server, blocks until shutdown.
func (s *Scheduler) Run() error {
	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 写 PID 文件
	pid := os.Getpid()
	if err := os.WriteFile(PIDPath(), []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		slog.Warn("failed to write pid file", "error", err)
	}
	defer os.Remove(PIDPath())

	// 启动 IPC server
	s.ipc = ipc.NewServer(SocketPath())
	s.registerHandlers()

	if err := s.ipc.Listen(); err != nil {
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer s.ipc.Close()
	defer os.Remove(SocketPath())

	// 加载已有任务
	tasks, err := s.store.ListActiveTasks()
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	for _, t := range tasks {
		s.scheduleTask(&t)
	}

	// 启动 cron
	s.cron.Start()
	s.started = time.Now()
	slog.Info("scheduler started", "tasks_loaded", len(tasks))

	// 设置一次性任务检查器
	go s.checkOneTimeTasks()

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig)

	// 优雅关闭
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	slog.Info("scheduler stopped")
	return nil
}

// scheduleTask adds a task to the cron scheduler.
func (s *Scheduler) scheduleTask(t *model.Task) {
	if t.Schedule == "" {
		// 一次性任务，由 checkOneTimeTasks 处理
		return
	}

	// 从 cron 表达式转换为 5 段（去掉秒）
	schedule := t.Schedule

	entryID, err := s.cron.AddFunc(schedule, func() {
		slog.Info("cron triggered", "task_id", t.ID, "task_name", t.Name)
		if err := s.executor.Run(t.ID); err != nil {
			slog.Error("task execution failed", "task_id", t.ID, "error", err)
		}
	})

	if err != nil {
		slog.Error("failed to schedule task", "task_id", t.ID, "schedule", schedule, "error", err)
		return
	}

	s.entryMap[t.ID] = entryID

	// 计算下次执行时间
	sched, _ := cron.ParseStandard(schedule)
	nextRun := sched.Next(time.Now())
	s.store.UpdateTaskNextRun(t.ID, nextRun)

	slog.Info("task scheduled", "task_id", t.ID, "name", t.Name, "schedule", schedule, "next_run", nextRun)
}

// checkOneTimeTasks periodically checks for one-time tasks that need to run.
func (s *Scheduler) checkOneTimeTasks() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		tasks, err := s.store.ListActiveTasks()
		if err != nil {
			slog.Error("failed to list tasks", "error", err)
			continue
		}

		now := time.Now()
		for i := range tasks {
			t := &tasks[i]
			if t.Schedule != "" {
				continue // 跳过 cron 任务
			}
			if t.RunAt.IsZero() {
				continue
			}
			if now.After(t.RunAt) || now.Equal(t.RunAt) {
				slog.Info("one-time task triggered", "task_id", t.ID, "task_name", t.Name)
				if err := s.executor.Run(t.ID); err != nil {
					slog.Error("one-time task execution failed", "task_id", t.ID, "error", err)
				}
			}
		}
	}
}

// removeTask removes a task from the scheduler.
func (s *Scheduler) removeTask(taskID string) {
	if entryID, ok := s.entryMap[taskID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, taskID)
	}
}

// registerHandlers registers all IPC method handlers.
func (s *Scheduler) registerHandlers() {
	s.ipc.Handle(ipc.MethodTaskCreate, s.handleTaskCreate)
	s.ipc.Handle(ipc.MethodTaskList, s.handleTaskList)
	s.ipc.Handle(ipc.MethodTaskGet, s.handleTaskGet)
	s.ipc.Handle(ipc.MethodTaskDelete, s.handleTaskDelete)
	s.ipc.Handle(ipc.MethodTaskPause, s.handleTaskPause)
	s.ipc.Handle(ipc.MethodTaskResume, s.handleTaskResume)
	s.ipc.Handle(ipc.MethodTaskRun, s.handleTaskRun)
	s.ipc.Handle(ipc.MethodTaskLogs, s.handleTaskLogs)
	s.ipc.Handle(ipc.MethodDaemonStatus, s.handleDaemonStatus)
}

func (s *Scheduler) handleTaskCreate(raw json.RawMessage) (interface{}, error) {
	var p ipc.CreateTaskParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	task := &model.Task{
		ID:        fmt.Sprintf("task_%s", time.Now().Format("20060102150405")),
		Name:      p.Name,
		Prompt:    p.Prompt,
		Schedule:  p.Schedule,
		WorkDir:   p.WorkDir,
		Status:    "active",
		CreatedAt: time.Now(),
	}

	if p.RunAt != "" {
		runAt, err := time.Parse(time.RFC3339, p.RunAt)
		if err != nil {
			return nil, fmt.Errorf("invalid run_at format, use RFC3339: %w", err)
		}
		task.RunAt = runAt
		task.NextRunAt = runAt
	}

	if err := s.store.CreateTask(task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	// 如果有 cron 表达式，注册调度
	if task.Schedule != "" {
		s.scheduleTask(task)
		// 从数据库读取调度器更新的 NextRunAt
		if updated, err := s.store.GetTask(task.ID); err == nil && updated != nil {
			task.NextRunAt = updated.NextRunAt
		}
	}

	return ipc.TaskResult{
		ID:        task.ID,
		Name:      task.Name,
		Status:    task.Status,
		Schedule:  task.Schedule,
		NextRunAt: task.NextRunAt.Format(time.RFC3339),
	}, nil
}

func (s *Scheduler) handleTaskList(json.RawMessage) (interface{}, error) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		return nil, err
	}

	var results []ipc.TaskResult
	for _, t := range tasks {
		nextRun := ""
		if !t.NextRunAt.IsZero() {
			nextRun = t.NextRunAt.Format(time.RFC3339)
		}
		lastRun := ""
		if !t.LastRunAt.IsZero() {
			lastRun = t.LastRunAt.Format(time.RFC3339)
		}
		results = append(results, ipc.TaskResult{
			ID:        t.ID,
			Name:      t.Name,
			Status:    t.Status,
			Schedule:  t.Schedule,
			NextRunAt: nextRun,
			LastRunAt: lastRun,
		})
	}
	if results == nil {
		results = []ipc.TaskResult{}
	}
	return results, nil
}

func (s *Scheduler) handleTaskGet(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	t, err := s.store.GetTask(p.ID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("task not found: %s", p.ID)
	}
	return ipc.TaskResult{
		ID:        t.ID,
		Name:      t.Name,
		Status:    t.Status,
		Schedule:  t.Schedule,
		NextRunAt: t.NextRunAt.Format(time.RFC3339),
		LastRunAt: t.LastRunAt.Format(time.RFC3339),
	}, nil
}

func (s *Scheduler) handleTaskDelete(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	s.removeTask(p.ID)
	if err := s.store.DeleteTask(p.ID); err != nil {
		return nil, err
	}
	return map[string]string{"status": "deleted"}, nil
}

func (s *Scheduler) handleTaskPause(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	s.removeTask(p.ID)
	if err := s.store.UpdateTaskStatus(p.ID, "paused"); err != nil {
		return nil, err
	}
	return map[string]string{"status": "paused"}, nil
}

func (s *Scheduler) handleTaskResume(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := s.store.UpdateTaskStatus(p.ID, "active"); err != nil {
		return nil, err
	}
	task, err := s.store.GetTask(p.ID)
	if err != nil {
		return nil, err
	}
	if task != nil && task.Schedule != "" {
		s.scheduleTask(task)
	}
	return map[string]string{"status": "active"}, nil
}

func (s *Scheduler) handleTaskRun(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := s.executor.Run(p.ID); err != nil {
		return nil, err
	}
	return map[string]string{"status": "running"}, nil
}

func (s *Scheduler) handleTaskLogs(raw json.RawMessage) (interface{}, error) {
	var p ipc.ListLogsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if p.Last {
		log, err := s.store.GetLastExecutionLog(p.TaskID)
		if err != nil {
			return nil, err
		}
		if log == nil {
			return []ipc.ExecutionLogResult{}, nil
		}
		return []ipc.ExecutionLogResult{
			{
				ID:        log.ID,
				TaskID:    log.TaskID,
				Output:    log.Output,
				Status:    log.Status,
				StartedAt: log.StartedAt.Format(time.RFC3339),
				EndedAt:   log.EndedAt.Format(time.RFC3339),
			},
		}, nil
	}

	logs, err := s.store.ListExecutionLogs(p.TaskID)
	if err != nil {
		return nil, err
	}

	var results []ipc.ExecutionLogResult
	for _, l := range logs {
		results = append(results, ipc.ExecutionLogResult{
			ID:        l.ID,
			TaskID:    l.TaskID,
			Output:    l.Output,
			Status:    l.Status,
			StartedAt: l.StartedAt.Format(time.RFC3339),
			EndedAt:   l.EndedAt.Format(time.RFC3339),
		})
	}
	if results == nil {
		results = []ipc.ExecutionLogResult{}
	}
	return results, nil
}

func (s *Scheduler) handleDaemonStatus(json.RawMessage) (interface{}, error) {
	running, pid := IsRunning()
	tasks, _ := s.store.ListTasks()
	activeTasks, _ := s.store.ListActiveTasks()

	result := ipc.DaemonStatusResult{
		Running:     running,
		TaskCount:   len(tasks),
		ActiveCount: len(activeTasks),
	}
	if running {
		result.PID = pid
		result.Uptime = time.Since(s.started).Round(time.Second).String()
	}
	return result, nil
}
