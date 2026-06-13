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
		return fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	execID := uuid.New().String()
	startedAt := time.Now()

	execLog := &model.ExecutionLog{
		ID:        execID,
		TaskID:    taskID,
		Status:    "running",
		StartedAt: startedAt,
	}
	if err := e.store.CreateExecutionLog(execLog); err != nil {
		return fmt.Errorf("create execution log: %w", err)
	}

	e.store.UpdateTaskLastRun(taskID, startedAt)

	slog.Info("executing task", "task_id", taskID, "task_name", task.Name, "exec_id", execID)

	go func() {
		result := agent.ClaudeWithTimeout(task.Prompt, task.WorkDir)
		endedAt := time.Now()

		status := "success"
		output := result.Output
		if result.Error != nil {
			status = "failed"
			output = fmt.Sprintf("%s\nError: %v", output, result.Error)
		}

		e.store.UpdateExecutionLog(execID, output, status, endedAt)

		if task.Schedule == "" {
			e.store.UpdateTaskStatus(taskID, "done")
		}

		slog.Info("task execution completed", "task_id", taskID, "exec_id", execID, "status", status, "duration", endedAt.Sub(startedAt))
	}()

	return nil
}
