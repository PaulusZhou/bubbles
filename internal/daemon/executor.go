package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/model"
	"github.com/pauluszhou/bubbles/internal/store"
)

// Executor runs a task by invoking the agent.
type Executor struct {
	store           *store.Store
	feishuNotifier  FeishuNotifier
	workDir         string
}

func NewExecutor(s *store.Store, cfg *config.Config) *Executor {
	return &Executor{store: s, workDir: cfg.WorkDir}
}

// SetFeishuNotifier sets the notifier for task completion events.
func (e *Executor) SetFeishuNotifier(notifier FeishuNotifier) {
	e.feishuNotifier = notifier
}

func (e *Executor) Run(taskID string) error {
	task, err := e.store.GetTask(taskID)
	if err != nil {
		slog.Error("executor: failed to get task", "task_id", taskID, "error", err)
		return fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		slog.Error("executor: task not found", "task_id", taskID)
		return fmt.Errorf("task not found: %s", taskID)
	}

	execID := uuid.New().String()
	startedAt := time.Now()

	slog.Info("executor: starting task execution",
		"task_id", taskID,
		"task_name", task.Name,
		"exec_id", execID,
		"work_dir", e.workDir,
		"prompt_len", len(task.Prompt),
	)

	execLog := &model.ExecutionLog{
		ID:        execID,
		TaskID:    taskID,
		Status:    "running",
		StartedAt: startedAt,
	}
	if err := e.store.CreateExecutionLog(execLog); err != nil {
		slog.Error("executor: failed to create execution log",
			"task_id", taskID,
			"exec_id", execID,
			"error", err,
		)
		return fmt.Errorf("create execution log: %w", err)
	}
	slog.Info("executor: execution log created", "task_id", taskID, "exec_id", execID)

	e.store.UpdateTaskLastRun(taskID, startedAt)
	slog.Info("executor: task last_run_at updated", "task_id", taskID, "last_run_at", startedAt)

	go func() {
		slog.Info("executor: invoking claude agent",
			"task_id", taskID,
			"exec_id", execID,
			"work_dir", e.workDir,
		)

		result := agent.ClaudeWithTimeout(task.Prompt, e.workDir)
		endedAt := time.Now()
		duration := endedAt.Sub(startedAt)

		status := "success"
		output := result.Output
		if result.Error != nil {
			status = "failed"
			output = fmt.Sprintf("%s\nError: %v", output, result.Error)
			slog.Error("executor: claude agent execution failed",
				"task_id", taskID,
				"exec_id", execID,
				"duration", duration,
				"error", result.Error,
			)
		} else {
			slog.Info("executor: claude agent execution succeeded",
				"task_id", taskID,
				"exec_id", execID,
				"duration", duration,
				"output_len", len(output),
			)
		}

		if err := e.store.UpdateExecutionLog(execID, output, status, endedAt); err != nil {
			slog.Error("executor: failed to update execution log",
				"task_id", taskID,
				"exec_id", execID,
				"status", status,
				"error", err,
			)
		} else {
			slog.Info("executor: execution log updated",
				"task_id", taskID,
				"exec_id", execID,
				"status", status,
				"duration", duration,
			)
		}

		// Note: one-time task status is marked as "done" by the scheduler BEFORE
		// dispatch to prevent re-triggering. Do not update it here.

		// Clean up: for one-time tasks (status = "done"), delete the task and its execution logs.
		// This runs after the task execution completes, whether successful or failed.
		if task.Status == "done" {
			if err := e.store.DeleteTask(taskID); err != nil {
				slog.Error("executor: failed to delete completed one-time task",
					"task_id", taskID,
					"exec_id", execID,
					"error", err,
				)
			} else {
				slog.Info("executor: completed one-time task deleted",
					"task_id", taskID,
					"exec_id", execID,
				)
			}
		}

		slog.Info("executor: task execution completed",
			"task_id", taskID,
			"exec_id", execID,
			"status", status,
			"duration", duration,
		)

		// Send Feishu notification if notifier is configured
		if e.feishuNotifier != nil {
			completion := TaskCompletion{
				TaskID:    taskID,
				TaskName:  task.Name,
				ExecID:    execID,
				Status:    status,
				Output:    output,
				Duration:  duration,
				StartedAt: startedAt,
				EndedAt:   endedAt,
			}
			if err := e.feishuNotifier.NotifyTaskCompletion(context.Background(), completion); err != nil {
				slog.Error("executor: failed to send feishu notification",
					"task_id", taskID,
					"exec_id", execID,
					"error", err,
				)
			} else {
				slog.Info("executor: feishu notification sent",
					"task_id", taskID,
					"exec_id", execID,
				)
			}
		}
	}()

	return nil
}
