package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/ipc"
	"github.com/pauluszhou/bubbles/internal/model"
	"github.com/pauluszhou/bubbles/internal/store"
)

func newTestScheduler(t *testing.T) (*Scheduler, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	cfg := &config.Config{
		ClaudePath: "claude",
		WorkDir:    dir,
	}
	sched := NewScheduler(s, cfg)
	cleanup := func() {
		s.Close()
	}
	return sched, s, cleanup
}

func makeTestTask(id, name string) *model.Task {
	return &model.Task{
		ID:        id,
		Name:      name,
		Prompt:    "test prompt",
		Status:    "active",
		CreatedAt: time.Now().Truncate(time.Second),
	}
}

// --- handleTaskCreate ---

func TestHandleTaskCreate_Basic(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "test task",
		Prompt: "hello world",
	}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult, ok := result.(ipc.TaskResult)
	if !ok {
		t.Fatalf("expected TaskResult, got %T", result)
	}
	if taskResult.Name != "test task" {
		t.Errorf("Name = %q, want %q", taskResult.Name, "test task")
	}
	if taskResult.Status != "active" {
		t.Errorf("Status = %q, want %q", taskResult.Status, "active")
	}
	if taskResult.ID == "" {
		t.Error("expected non-empty task ID")
	}
}

func TestHandleTaskCreate_WithSchedule(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:     "cron task",
		Prompt:   "daily check",
		Schedule: "0 9 * * *",
	}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.Schedule != "0 9 * * *" {
		t.Errorf("Schedule = %q, want %q", taskResult.Schedule, "0 9 * * *")
	}
	if taskResult.NextRunAt == "" {
		t.Error("expected non-empty NextRunAt for cron task")
	}
}

func TestHandleTaskCreate_WithRunAt_LocalTime(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "one-time task",
		Prompt: "do something",
		RunAt:  "2026-06-20T14:30:00",
	}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.NextRunAt == "" {
		t.Error("expected non-empty NextRunAt for one-time task")
	}
}

func TestHandleTaskCreate_WithRunAt_RFC3339(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "one-time task",
		Prompt: "do something",
		RunAt:  "2026-06-20T14:30:00Z",
	}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.NextRunAt == "" {
		t.Error("expected non-empty NextRunAt for one-time task")
	}
}

func TestHandleTaskCreate_InvalidRunAt(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "bad task",
		Prompt: "hello",
		RunAt:  "not-a-date",
	}
	raw, _ := json.Marshal(params)

	_, err := sched.handleTaskCreate(raw)
	if err == nil {
		t.Fatal("expected error for invalid RunAt")
	}
}

func TestHandleTaskCreate_InvalidParams(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	// Send invalid JSON
	_, err := sched.handleTaskCreate(json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON params")
	}
}

// --- handleTaskList ---

func TestHandleTaskList_Empty(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	result, err := sched.handleTaskList(nil)
	if err != nil {
		t.Fatalf("handleTaskList: %v", err)
	}

	results, ok := result.([]ipc.TaskResult)
	if !ok {
		t.Fatalf("expected []TaskResult, got %T", result)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(results))
	}
}

func TestHandleTaskList_WithTasks(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "first"))
	s.CreateTask(makeTestTask("task_2", "second"))

	result, err := sched.handleTaskList(nil)
	if err != nil {
		t.Fatalf("handleTaskList: %v", err)
	}

	results := result.([]ipc.TaskResult)
	if len(results) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(results))
	}
}

// --- handleTaskGet ---

func TestHandleTaskGet_Found(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	params := ipc.TaskIDParams{ID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskGet(raw)
	if err != nil {
		t.Fatalf("handleTaskGet: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.ID != "task_1" {
		t.Errorf("ID = %q, want %q", taskResult.ID, "task_1")
	}
	if taskResult.Name != "test" {
		t.Errorf("Name = %q, want %q", taskResult.Name, "test")
	}
}

func TestHandleTaskGet_NotFound(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.TaskIDParams{ID: "nonexistent"}
	raw, _ := json.Marshal(params)

	_, err := sched.handleTaskGet(raw)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestHandleTaskGet_InvalidParams(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	_, err := sched.handleTaskGet(json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid params")
	}
}

// --- handleTaskDelete ---

func TestHandleTaskDelete_Success(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	params := ipc.TaskIDParams{ID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskDelete(raw)
	if err != nil {
		t.Fatalf("handleTaskDelete: %v", err)
	}

	resultMap, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if resultMap["status"] != "deleted" {
		t.Errorf("status = %q, want %q", resultMap["status"], "deleted")
	}
}

func TestHandleTaskDelete_NotFound(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.TaskIDParams{ID: "nonexistent"}
	raw, _ := json.Marshal(params)

	// DeleteTask is idempotent, should not error
	result, err := sched.handleTaskDelete(raw)
	if err != nil {
		t.Fatalf("handleTaskDelete: %v", err)
	}
	_ = result
}

// --- handleTaskPause ---

func TestHandleTaskPause_Success(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	params := ipc.TaskIDParams{ID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskPause(raw)
	if err != nil {
		t.Fatalf("handleTaskPause: %v", err)
	}

	resultMap := result.(map[string]string)
	if resultMap["status"] != "paused" {
		t.Errorf("status = %q, want %q", resultMap["status"], "paused")
	}

	// Verify task status in store
	task, _ := s.GetTask("task_1")
	if task.Status != "paused" {
		t.Errorf("task status = %q, want %q", task.Status, "paused")
	}
}

// --- handleTaskResume ---

func TestHandleTaskResume_Success(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	task := makeTestTask("task_1", "test")
	task.Status = "paused"
	s.CreateTask(task)
	s.UpdateTaskStatus("task_1", "paused")

	params := ipc.TaskIDParams{ID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskResume(raw)
	if err != nil {
		t.Fatalf("handleTaskResume: %v", err)
	}

	resultMap := result.(map[string]string)
	if resultMap["status"] != "active" {
		t.Errorf("status = %q, want %q", resultMap["status"], "active")
	}

	// Verify task status in store
	got, _ := s.GetTask("task_1")
	if got.Status != "active" {
		t.Errorf("task status = %q, want %q", got.Status, "active")
	}
}

// --- handleTaskRun ---

func TestHandleTaskRun_Success(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	params := ipc.TaskIDParams{ID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskRun(raw)
	if err != nil {
		t.Fatalf("handleTaskRun: %v", err)
	}

	resultMap := result.(map[string]string)
	if resultMap["status"] != "running" {
		t.Errorf("status = %q, want %q", resultMap["status"], "running")
	}
}

func TestHandleTaskRun_TaskNotFound(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.TaskIDParams{ID: "nonexistent"}
	raw, _ := json.Marshal(params)

	_, err := sched.handleTaskRun(raw)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

// --- handleTaskLogs ---

func TestHandleTaskLogs_Empty(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	params := ipc.ListLogsParams{TaskID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskLogs(raw)
	if err != nil {
		t.Fatalf("handleTaskLogs: %v", err)
	}

	results := result.([]ipc.ExecutionLogResult)
	if len(results) != 0 {
		t.Errorf("expected 0 logs, got %d", len(results))
	}
}

func TestHandleTaskLogs_WithLogs(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))
	s.CreateExecutionLog(&model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "task_1",
		Output:    "output",
		Status:    "success",
		StartedAt: time.Now(),
	})

	params := ipc.ListLogsParams{TaskID: "task_1"}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskLogs(raw)
	if err != nil {
		t.Fatalf("handleTaskLogs: %v", err)
	}

	results := result.([]ipc.ExecutionLogResult)
	if len(results) != 1 {
		t.Fatalf("expected 1 log, got %d", len(results))
	}
	if results[0].ID != "log_1" {
		t.Errorf("ID = %q, want %q", results[0].ID, "log_1")
	}
}

func TestHandleTaskLogs_LastOnly(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))
	for i := 0; i < 3; i++ {
		s.CreateExecutionLog(&model.ExecutionLog{
			ID:        fmt.Sprintf("log_%d", i),
			TaskID:    "task_1",
			Status:    "success",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	params := ipc.ListLogsParams{TaskID: "task_1", Last: true}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskLogs(raw)
	if err != nil {
		t.Fatalf("handleTaskLogs: %v", err)
	}

	results := result.([]ipc.ExecutionLogResult)
	if len(results) != 1 {
		t.Fatalf("expected 1 log with Last=true, got %d", len(results))
	}
	// Should be the most recent (log_2)
	if results[0].ID != "log_2" {
		t.Errorf("ID = %q, want %q", results[0].ID, "log_2")
	}
}

// --- handleDaemonStatus ---

func TestHandleDaemonStatus(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	// Create some tasks
	s.CreateTask(makeTestTask("task_1", "active task"))
	paused := makeTestTask("task_2", "paused task")
	paused.Status = "paused"
	s.CreateTask(paused)

	result, err := sched.handleDaemonStatus(nil)
	if err != nil {
		t.Fatalf("handleDaemonStatus: %v", err)
	}

	status := result.(ipc.DaemonStatusResult)
	if status.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want %d", status.TaskCount, 2)
	}
	if status.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want %d", status.ActiveCount, 1)
	}
}

// --- PauseTask / ResumeTask / DeleteTask ---

func TestPauseTask(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	if err := sched.PauseTask("task_1"); err != nil {
		t.Fatalf("PauseTask: %v", err)
	}

	task, _ := s.GetTask("task_1")
	if task.Status != "paused" {
		t.Errorf("status = %q, want %q", task.Status, "paused")
	}
}

func TestResumeTask(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	task := makeTestTask("task_1", "test")
	task.Status = "paused"
	s.CreateTask(task)
	s.UpdateTaskStatus("task_1", "paused")

	if err := sched.ResumeTask("task_1"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}

	got, _ := s.GetTask("task_1")
	if got.Status != "active" {
		t.Errorf("status = %q, want %q", got.Status, "active")
	}
}

func TestDeleteTask(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test"))

	if err := sched.DeleteTask("task_1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Task should be deleted
	_, err := s.GetTask("task_1")
	if err == nil {
		t.Fatal("expected error for deleted task")
	}
}

// --- GetAllTaskSummary ---

func TestGetAllTaskSummary_Empty(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	summaries, err := sched.GetAllTaskSummary()
	if err != nil {
		t.Fatalf("GetAllTaskSummary: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestGetAllTaskSummary_WithTasks(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "active"))
	paused := makeTestTask("task_2", "paused")
	paused.Status = "paused"
	s.CreateTask(paused)

	summaries, err := sched.GetAllTaskSummary()
	if err != nil {
		t.Fatalf("GetAllTaskSummary: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
}

// --- scheduleTask ---

func TestScheduleTask_CronTask(t *testing.T) {
	sched, s, cleanup := newTestScheduler(t)
	defer cleanup()

	task := makeTestTask("task_cron", "cron task")
	task.Schedule = "0 9 * * *"
	s.CreateTask(task)

	// scheduleTask should add to cron and update next_run_at
	sched.scheduleTask(task)

	// Verify next_run_at was updated
	got, _ := s.GetTask("task_cron")
	if got.NextRunAt.IsZero() {
		t.Error("expected NextRunAt to be set for cron task")
	}
}

func TestScheduleTask_OneTimeTask(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	task := makeTestTask("task_once", "one-time")
	// No schedule — should be skipped by scheduleTask
	sched.scheduleTask(task)
	// No error means success (one-time tasks are handled by checkOneTimeTasks)
}

// --- removeTask ---

func TestRemoveTask(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	task := makeTestTask("task_1", "test")
	task.Schedule = "0 9 * * *"

	// Add to entryMap manually
	sched.entryMapMu.Lock()
	sched.entryMap["task_1"] = 1
	sched.entryMapMu.Unlock()

	sched.removeTask("task_1")

	sched.entryMapMu.RLock()
	_, exists := sched.entryMap["task_1"]
	sched.entryMapMu.RUnlock()

	if exists {
		t.Error("expected task to be removed from entryMap")
	}
}

func TestRemoveTask_NotInMap(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	// Should not panic
	sched.removeTask("nonexistent")
}

// --- Edge cases ---

func TestHandleTaskCreate_EmptyName(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "",
		Prompt: "hello",
	}
	raw, _ := json.Marshal(params)

	// Empty name should still succeed (no validation)
	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.Name != "" {
		t.Errorf("expected empty name, got %q", taskResult.Name)
	}
}

func TestHandleTaskCreate_EmptyPrompt(t *testing.T) {
	sched, _, cleanup := newTestScheduler(t)
	defer cleanup()

	params := ipc.CreateTaskParams{
		Name:   "test",
		Prompt: "",
	}
	raw, _ := json.Marshal(params)

	result, err := sched.handleTaskCreate(raw)
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}

	taskResult := result.(ipc.TaskResult)
	if taskResult.Name != "test" {
		t.Errorf("Name = %q, want %q", taskResult.Name, "test")
	}
}
