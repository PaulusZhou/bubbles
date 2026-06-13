package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/model"
	"github.com/pauluszhou/bubbles/internal/store"
)

// Executor runs a task by invoking the agent.
type Executor struct {
	store *store.Store
}

func NewExecutor(s *store.Store) *Executor {
	return &Executor{store: s}
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
		"work_dir", task.WorkDir,
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
			"work_dir", task.WorkDir,
		)

		result := agent.ClaudeWithTimeout(task.Prompt, task.WorkDir)
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

		if task.Schedule == "" {
			if err := e.store.UpdateTaskStatus(taskID, "done"); err != nil {
				slog.Error("executor: failed to update one-time task status to done",
					"task_id", taskID,
					"error", err,
				)
			} else {
				slog.Info("executor: one-time task marked as done", "task_id", taskID)
			}
		}

		slog.Info("executor: task execution completed",
			"task_id", taskID,
			"exec_id", execID,
			"status", status,
			"duration", duration,
		)
	}()

	return nil
}
