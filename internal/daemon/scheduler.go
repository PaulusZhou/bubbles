package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/pauluszhou/bubbles/internal/ipc"
	"github.com/pauluszhou/bubbles/internal/model"
	"github.com/pauluszhou/bubbles/internal/store"
)

// FeishuStopper is an interface for stopping the Feishu channel during shutdown.
type FeishuStopper interface {
	Stop(ctx context.Context) error
}

// Scheduler manages cron jobs and one-time tasks.
type Scheduler struct {
	cron          *cron.Cron
	store         *store.Store
	executor      *Executor
	ipc           *ipc.Server
	started       time.Time
	entryMap      map[string]cron.EntryID // taskID -> cron entry ID
	feishuStopper FeishuStopper
}

func NewScheduler(s *store.Store) *Scheduler {
	return &Scheduler{
		cron:     cron.New(), // 标准 5 段 cron
		store:    s,
		executor: NewExecutor(s),
		entryMap: make(map[string]cron.EntryID),
	}
}

// SetFeishuStopper sets the Feishu channel stopper for graceful shutdown.
func (s *Scheduler) SetFeishuStopper(stopper FeishuStopper) {
	s.feishuStopper = stopper
	slog.Info("scheduler: feishu stopper registered")
}

// Run starts the scheduler and IPC server, blocks until shutdown.
func (s *Scheduler) Run() error {
	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		slog.Error("scheduler: failed to create data dir", "dir", dataDir, "error", err)
		return fmt.Errorf("create data dir: %w", err)
	}
	slog.Info("scheduler: data directory ready", "dir", dataDir)

	// 写 PID 文件
	pid := os.Getpid()
	if err := os.WriteFile(PIDPath(), []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		slog.Warn("scheduler: failed to write pid file", "path", PIDPath(), "error", err)
	} else {
		slog.Info("scheduler: pid file written", "path", PIDPath(), "pid", pid)
	}
	defer os.Remove(PIDPath())

	// 启动 IPC server
	s.ipc = ipc.NewServer(SocketPath())
	s.registerHandlers()
	slog.Info("scheduler: ipc handlers registered")

	if err := s.ipc.Listen(); err != nil {
		slog.Error("scheduler: failed to start ipc server", "error", err)
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer s.ipc.Close()
	defer os.Remove(SocketPath())
	slog.Info("scheduler: ipc server started", "socket", SocketPath())

	// 加载已有任务
	tasks, err := s.store.ListActiveTasks()
	if err != nil {
		slog.Error("scheduler: failed to load active tasks", "error", err)
		return fmt.Errorf("load tasks: %w", err)
	}
	for i := range tasks {
		s.scheduleTask(&tasks[i])
	}
	slog.Info("scheduler: active tasks loaded", "count", len(tasks))

	// 启动 cron
	s.cron.Start()
	s.started = time.Now()
	slog.Info("scheduler: cron scheduler started", "tasks_loaded", len(tasks))

	// 设置一次性任务检查器
	go s.checkOneTimeTasks()
	slog.Info("scheduler: one-time task checker started")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("scheduler: received shutdown signal", "signal", sig)

	// 优雅关闭
	slog.Info("scheduler: stopping cron scheduler...")
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	slog.Info("scheduler: cron scheduler stopped")

	// 停止飞书 Channel
	if s.feishuStopper != nil {
		slog.Info("scheduler: stopping feishu channel...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.feishuStopper.Stop(shutdownCtx); err != nil {
			slog.Error("scheduler: failed to stop feishu channel", "error", err)
		} else {
			slog.Info("scheduler: feishu channel stopped")
		}
	}

	slog.Info("scheduler: shutdown complete")
	return nil
}

// scheduleTask adds a task to the cron scheduler.
func (s *Scheduler) scheduleTask(t *model.Task) {
	if t.Schedule == "" {
		// 一次性任务，由 checkOneTimeTasks 处理
		return
	}

	schedule := t.Schedule

	entryID, err := s.cron.AddFunc(schedule, func() {
		slog.Info("scheduler: cron job triggered",
			"task_id", t.ID,
			"task_name", t.Name,
			"schedule", schedule,
		)
		if err := s.executor.Run(t.ID); err != nil {
			slog.Error("scheduler: cron job execution dispatch failed",
				"task_id", t.ID,
				"task_name", t.Name,
				"error", err,
			)
		} else {
			slog.Info("scheduler: cron job execution dispatched",
				"task_id", t.ID,
				"task_name", t.Name,
			)
		}
	})

	if err != nil {
		slog.Error("scheduler: failed to add cron entry",
			"task_id", t.ID,
			"schedule", schedule,
			"error", err,
		)
		return
	}

	s.entryMap[t.ID] = entryID

	// 计算下次执行时间
	sched, _ := cron.ParseStandard(schedule)
	nextRun := sched.Next(time.Now())
	s.store.UpdateTaskNextRun(t.ID, nextRun)

	slog.Info("scheduler: task scheduled",
		"task_id", t.ID,
		"name", t.Name,
		"schedule", schedule,
		"next_run", nextRun,
		"cron_entry_id", entryID,
	)
}

// checkOneTimeTasks periodically checks for one-time tasks that need to run.
func (s *Scheduler) checkOneTimeTasks() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		tasks, err := s.store.ListActiveTasks()
		if err != nil {
			slog.Error("scheduler: one-time checker failed to list tasks", "error", err)
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
				slog.Info("scheduler: one-time task triggered",
					"task_id", t.ID,
					"task_name", t.Name,
					"scheduled_at", t.RunAt,
					"delay", now.Sub(t.RunAt).Round(time.Second),
				)
				if err := s.executor.Run(t.ID); err != nil {
					slog.Error("scheduler: one-time task execution dispatch failed",
						"task_id", t.ID,
						"task_name", t.Name,
						"error", err,
					)
				} else {
					slog.Info("scheduler: one-time task execution dispatched",
						"task_id", t.ID,
						"task_name", t.Name,
					)
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
		slog.Info("scheduler: task removed from cron",
			"task_id", taskID,
			"cron_entry_id", entryID,
		)
	} else {
		slog.Debug("scheduler: task was not in cron (one-time task or already removed)",
			"task_id", taskID,
		)
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
		slog.Error("ipc: task.create invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.create received",
		"name", p.Name,
		"schedule", p.Schedule,
		"run_at", p.RunAt,
		"work_dir", p.WorkDir,
		"prompt_len", len(p.Prompt),
	)

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
			slog.Error("ipc: task.create invalid run_at", "run_at", p.RunAt, "error", err)
			return nil, fmt.Errorf("invalid run_at format, use RFC3339: %w", err)
		}
		task.RunAt = runAt
		task.NextRunAt = runAt
	}

	if err := s.store.CreateTask(task); err != nil {
		slog.Error("ipc: task.create db error", "task_id", task.ID, "error", err)
		return nil, fmt.Errorf("create task: %w", err)
	}
	slog.Info("ipc: task.create persisted", "task_id", task.ID, "name", task.Name)

	if task.Schedule != "" {
		s.scheduleTask(task)
		if updated, err := s.store.GetTask(task.ID); err == nil && updated != nil {
			task.NextRunAt = updated.NextRunAt
		}
	}

	slog.Info("ipc: task.create completed",
		"task_id", task.ID,
		"name", task.Name,
		"status", task.Status,
		"next_run", task.NextRunAt.Format(time.RFC3339),
	)

	return ipc.TaskResult{
		ID:        task.ID,
		Name:      task.Name,
		Status:    task.Status,
		Schedule:  task.Schedule,
		NextRunAt: task.NextRunAt.Format(time.RFC3339),
	}, nil
}

func (s *Scheduler) handleTaskList(json.RawMessage) (interface{}, error) {
	slog.Info("ipc: task.list received")

	tasks, err := s.store.ListTasks()
	if err != nil {
		slog.Error("ipc: task.list db error", "error", err)
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

	slog.Info("ipc: task.list completed", "count", len(results))
	return results, nil
}

func (s *Scheduler) handleTaskGet(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Error("ipc: task.get invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.get received", "task_id", p.ID)

	t, err := s.store.GetTask(p.ID)
	if err != nil {
		slog.Error("ipc: task.get db error", "task_id", p.ID, "error", err)
		return nil, err
	}
	if t == nil {
		slog.Warn("ipc: task.get not found", "task_id", p.ID)
		return nil, fmt.Errorf("task not found: %s", p.ID)
	}

	slog.Info("ipc: task.get completed", "task_id", t.ID, "name", t.Name, "status", t.Status)
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
		slog.Error("ipc: task.delete invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.delete received", "task_id", p.ID)

	s.removeTask(p.ID)
	if err := s.store.DeleteTask(p.ID); err != nil {
		slog.Error("ipc: task.delete db error", "task_id", p.ID, "error", err)
		return nil, err
	}

	slog.Info("ipc: task.delete completed", "task_id", p.ID)
	return map[string]string{"status": "deleted"}, nil
}

func (s *Scheduler) handleTaskPause(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Error("ipc: task.pause invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.pause received", "task_id", p.ID)

	s.removeTask(p.ID)
	if err := s.store.UpdateTaskStatus(p.ID, "paused"); err != nil {
		slog.Error("ipc: task.pause db error", "task_id", p.ID, "error", err)
		return nil, err
	}

	slog.Info("ipc: task.pause completed", "task_id", p.ID)
	return map[string]string{"status": "paused"}, nil
}

func (s *Scheduler) handleTaskResume(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Error("ipc: task.resume invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.resume received", "task_id", p.ID)

	if err := s.store.UpdateTaskStatus(p.ID, "active"); err != nil {
		slog.Error("ipc: task.resume db error", "task_id", p.ID, "error", err)
		return nil, err
	}

	task, err := s.store.GetTask(p.ID)
	if err != nil {
		slog.Error("ipc: task.resume failed to re-fetch task", "task_id", p.ID, "error", err)
		return nil, err
	}
	if task != nil && task.Schedule != "" {
		s.scheduleTask(task)
	}

	slog.Info("ipc: task.resume completed", "task_id", p.ID)
	return map[string]string{"status": "active"}, nil
}

func (s *Scheduler) handleTaskRun(raw json.RawMessage) (interface{}, error) {
	var p ipc.TaskIDParams
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Error("ipc: task.run invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.run received", "task_id", p.ID)

	if err := s.executor.Run(p.ID); err != nil {
		slog.Error("ipc: task.run dispatch failed", "task_id", p.ID, "error", err)
		return nil, err
	}

	slog.Info("ipc: task.run dispatched", "task_id", p.ID)
	return map[string]string{"status": "running"}, nil
}

func (s *Scheduler) handleTaskLogs(raw json.RawMessage) (interface{}, error) {
	var p ipc.ListLogsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		slog.Error("ipc: task.logs invalid params", "error", err)
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	slog.Info("ipc: task.logs received", "task_id", p.TaskID, "last", p.Last)

	if p.Last {
		log, err := s.store.GetLastExecutionLog(p.TaskID)
		if err != nil {
			slog.Error("ipc: task.logs db error", "task_id", p.TaskID, "error", err)
			return nil, err
		}
		if log == nil {
			slog.Info("ipc: task.logs no logs found", "task_id", p.TaskID)
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
		slog.Error("ipc: task.logs db error", "task_id", p.TaskID, "error", err)
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

	slog.Info("ipc: task.logs completed", "task_id", p.TaskID, "count", len(results))
	return results, nil
}

// FormatActiveTaskList returns a Markdown-formatted list of all active tasks,
// split into cron tasks and one-time tasks. Pure domain logic, no Feishu dependency.
func (s *Scheduler) FormatActiveTaskList() (string, error) {
	tasks, err := s.store.ListActiveTasks()
	if err != nil {
		return "", err
	}

	if len(tasks) == 0 {
		return "📋 当前没有活跃任务", nil
	}

	var cronTasks, oneTimeTasks []model.Task
	for _, t := range tasks {
		if t.Schedule != "" {
			cronTasks = append(cronTasks, t)
		} else {
			oneTimeTasks = append(oneTimeTasks, t)
		}
	}

	var sb strings.Builder
	sb.WriteString("📋 **任务列表**\n")

	if len(cronTasks) > 0 {
		sb.WriteString("\n⏰ **定时任务**\n")
		for _, t := range cronTasks {
			nextRun := "-"
			if !t.NextRunAt.IsZero() {
				nextRun = t.NextRunAt.Format("2006-01-02 15:04")
			}
			name := t.Name
			if name == "" {
				name = t.ID
			}
			sb.WriteString(fmt.Sprintf("  • %s — Cron: %s，下次执行: %s\n", name, t.Schedule, nextRun))
		}
	}

	if len(oneTimeTasks) > 0 {
		sb.WriteString("\n📌 **一次性任务**\n")
		for _, t := range oneTimeTasks {
			runAt := "-"
			if !t.RunAt.IsZero() {
				runAt = t.RunAt.Format("2006-01-02 15:04")
			}
			name := t.Name
			if name == "" {
				name = t.ID
			}
			sb.WriteString(fmt.Sprintf("  • %s — 计划时间: %s，状态: %s\n", name, runAt, t.Status))
		}
	}

	sb.WriteString(fmt.Sprintf("\n共 **%d** 个任务", len(tasks)))
	return sb.String(), nil
}

func (s *Scheduler) handleDaemonStatus(json.RawMessage) (interface{}, error) {
	slog.Info("ipc: daemon.status received")

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

	slog.Info("ipc: daemon.status completed",
		"running", running,
		"pid", pid,
		"tasks", len(tasks),
		"active", len(activeTasks),
		"uptime", result.Uptime,
	)
	return result, nil
}
