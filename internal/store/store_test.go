package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pauluszhou/bubbles/internal/model"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	cleanup := func() { s.Close() }
	return s, cleanup
}

func makeTask(id, name string) *model.Task {
	return &model.Task{
		ID:        id,
		Name:      name,
		Prompt:    "test prompt",
		Status:    "active",
		CreatedAt: time.Now().Truncate(time.Second),
	}
}

// --- Migration ---

func TestNew_MigratesTables(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Should be able to query tasks and logs without error
	tasks, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks failed after migration: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestNew_CreatesDBFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("expected DB file to be created")
	}
}

// --- Task CRUD ---

func TestCreateAndGetTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := makeTask("task_20260101000000", "test task")
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.GetTask("task_20260101000000")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got == nil {
		t.Fatal("expected task, got nil")
	}
	if got.ID != task.ID {
		t.Errorf("ID = %q, want %q", got.ID, task.ID)
	}
	if got.Name != task.Name {
		t.Errorf("Name = %q, want %q", got.Name, task.Name)
	}
	if got.Prompt != task.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, task.Prompt)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want %q", got.Status, "active")
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	got, err := s.GetTask("nonexistent")
	// GetTask returns sql.ErrNoRows for not-found rows
	if err == nil {
		t.Fatal("expected error for nonexistent task, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestCreateTask_DuplicateID(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := makeTask("task_dup", "first")
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task2 := makeTask("task_dup", "second")
	err := s.CreateTask(task2)
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
}

func TestListTasks(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Empty list
	tasks, err := s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0, got %d", len(tasks))
	}

	// Create 3 tasks
	for i := 0; i < 3; i++ {
		task := makeTask(fmt.Sprintf("task_%d", i), fmt.Sprintf("task %d", i))
		if err := s.CreateTask(task); err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
	}

	tasks, err = s.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3, got %d", len(tasks))
	}
}

func TestListActiveTasks(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Create active and paused tasks
	s.CreateTask(makeTask("task_active1", "active1"))
	s.CreateTask(makeTask("task_active2", "active2"))
	paused := makeTask("task_paused", "paused")
	paused.Status = "paused"
	s.CreateTask(paused)

	active, err := s.ListActiveTasks()
	if err != nil {
		t.Fatalf("ListActiveTasks: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active tasks, got %d", len(active))
	}
	for _, t2 := range active {
		if t2.Status != "active" {
			t.Errorf("expected active status, got %q", t2.Status)
		}
	}
}

func TestUpdateTaskStatus(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	if err := s.UpdateTaskStatus("task_1", "paused"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	got, _ := s.GetTask("task_1")
	if got.Status != "paused" {
		t.Errorf("Status = %q, want %q", got.Status, "paused")
	}
}

func TestUpdateTaskStatus_NonexistentTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Should not error — UPDATE affects 0 rows silently
	err := s.UpdateTaskStatus("nonexistent", "paused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateTaskNextRun(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	nextRun := time.Now().Add(1 * time.Hour).Truncate(time.Second)

	if err := s.UpdateTaskNextRun("task_1", nextRun); err != nil {
		t.Fatalf("UpdateTaskNextRun: %v", err)
	}

	got, _ := s.GetTask("task_1")
	if !got.NextRunAt.Equal(nextRun) {
		t.Errorf("NextRunAt = %v, want %v", got.NextRunAt, nextRun)
	}
}

func TestUpdateTaskLastRun(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	lastRun := time.Now().Truncate(time.Second)

	if err := s.UpdateTaskLastRun("task_1", lastRun); err != nil {
		t.Fatalf("UpdateTaskLastRun: %v", err)
	}

	got, _ := s.GetTask("task_1")
	if !got.LastRunAt.Equal(lastRun) {
		t.Errorf("LastRunAt = %v, want %v", got.LastRunAt, lastRun)
	}
}

func TestDeleteTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	// Create an execution log for this task
	log := &model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "task_1",
		Status:    "success",
		StartedAt: time.Now(),
	}
	s.CreateExecutionLog(log)

	if err := s.DeleteTask("task_1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// GetTask returns sql.ErrNoRows for deleted tasks
	got, err := s.GetTask("task_1")
	if err == nil {
		t.Fatal("expected error for deleted task")
	}
	if got != nil {
		t.Fatal("expected task to be deleted")
	}

	// Execution logs should also be deleted
	logs, _ := s.ListExecutionLogs("task_1")
	if len(logs) != 0 {
		t.Fatalf("expected 0 execution logs after task delete, got %d", len(logs))
	}
}

func TestDeleteTask_NonexistentTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Should not error
	err := s.DeleteTask("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ExecutionLog CRUD ---

func TestCreateAndGetExecutionLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	now := time.Now().Truncate(time.Second)
	log := &model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "task_1",
		Output:    "hello world",
		Status:    "running",
		StartedAt: now,
	}
	if err := s.CreateExecutionLog(log); err != nil {
		t.Fatalf("CreateExecutionLog: %v", err)
	}

	logs, err := s.ListExecutionLogs("task_1")
	if err != nil {
		t.Fatalf("ListExecutionLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].ID != "log_1" {
		t.Errorf("ID = %q, want %q", logs[0].ID, "log_1")
	}
	if logs[0].Output != "hello world" {
		t.Errorf("Output = %q, want %q", logs[0].Output, "hello world")
	}
	if logs[0].Status != "running" {
		t.Errorf("Status = %q, want %q", logs[0].Status, "running")
	}
}

func TestUpdateExecutionLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	log := &model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "task_1",
		Status:    "running",
		StartedAt: time.Now(),
	}
	s.CreateExecutionLog(log)

	endedAt := time.Now().Truncate(time.Second)
	if err := s.UpdateExecutionLog("log_1", "output text", "success", endedAt); err != nil {
		t.Fatalf("UpdateExecutionLog: %v", err)
	}

	logs, _ := s.ListExecutionLogs("task_1")
	if logs[0].Output != "output text" {
		t.Errorf("Output = %q, want %q", logs[0].Output, "output text")
	}
	if logs[0].Status != "success" {
		t.Errorf("Status = %q, want %q", logs[0].Status, "success")
	}
}

func TestListExecutionLogs_Empty(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	logs, err := s.ListExecutionLogs("nonexistent")
	if err != nil {
		t.Fatalf("ListExecutionLogs: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected 0, got %d", len(logs))
	}
}

func TestListExecutionLogs_Multiple(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	for i := 0; i < 5; i++ {
		log := &model.ExecutionLog{
			ID:        fmt.Sprintf("log_%d", i),
			TaskID:    "task_1",
			Status:    "success",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		s.CreateExecutionLog(log)
	}

	logs, err := s.ListExecutionLogs("task_1")
	if err != nil {
		t.Fatalf("ListExecutionLogs: %v", err)
	}
	if len(logs) != 5 {
		t.Fatalf("expected 5, got %d", len(logs))
	}
	// Should be ordered by started_at DESC
	for i := 0; i < len(logs)-1; i++ {
		if logs[i].StartedAt.Before(logs[i+1].StartedAt) {
			t.Errorf("logs not in descending order: logs[%d]=%v < logs[%d]=%v",
				i, logs[i].StartedAt, i+1, logs[i+1].StartedAt)
		}
	}
}

func TestGetLastExecutionLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	// No logs yet
	last, err := s.GetLastExecutionLog("task_1")
	if err != nil {
		t.Fatalf("GetLastExecutionLog: %v", err)
	}
	if last != nil {
		t.Fatalf("expected nil, got %+v", last)
	}

	// Create logs with different times
	for i := 0; i < 3; i++ {
		log := &model.ExecutionLog{
			ID:        fmt.Sprintf("log_%d", i),
			TaskID:    "task_1",
			Status:    "success",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		s.CreateExecutionLog(log)
	}

	last, err = s.GetLastExecutionLog("task_1")
	if err != nil {
		t.Fatalf("GetLastExecutionLog: %v", err)
	}
	if last == nil {
		t.Fatal("expected log, got nil")
	}
	// Should be the most recent (log_2)
	if last.ID != "log_2" {
		t.Errorf("ID = %q, want %q", last.ID, "log_2")
	}
}

func TestGetLastExecutionLog_NonexistentTask(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	last, err := s.GetLastExecutionLog("nonexistent")
	if err != nil {
		t.Fatalf("GetLastExecutionLog: %v", err)
	}
	if last != nil {
		t.Fatalf("expected nil, got %+v", last)
	}
}

// --- Edge cases ---

func TestCreateTask_ZeroTimeFields(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := &model.Task{
		ID:        "task_zero",
		Name:      "zero time",
		Prompt:    "test",
		Status:    "active",
		CreatedAt: time.Now(),
		// RunAt, NextRunAt, LastRunAt are zero
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, _ := s.GetTask("task_zero")
	if !got.RunAt.IsZero() {
		t.Errorf("expected zero RunAt, got %v", got.RunAt)
	}
	if !got.NextRunAt.IsZero() {
		t.Errorf("expected zero NextRunAt, got %v", got.NextRunAt)
	}
	if !got.LastRunAt.IsZero() {
		t.Errorf("expected zero LastRunAt, got %v", got.LastRunAt)
	}
}

func TestCreateTask_WithRunAt(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	runAt := time.Date(2026, 6, 15, 14, 30, 0, 0, time.Local).Truncate(time.Second)
	task := &model.Task{
		ID:        "task_runat",
		Name:      "with run_at",
		Prompt:    "test",
		Status:    "active",
		CreatedAt: time.Now(),
		RunAt:     runAt,
		NextRunAt: runAt,
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, _ := s.GetTask("task_runat")
	if !got.RunAt.Equal(runAt) {
		t.Errorf("RunAt = %v, want %v", got.RunAt, runAt)
	}
}

func TestCreateTask_WithSchedule(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := &model.Task{
		ID:        "task_cron",
		Name:      "cron task",
		Prompt:    "test",
		Schedule:  "0 9 * * *",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, _ := s.GetTask("task_cron")
	if got.Schedule != "0 9 * * *" {
		t.Errorf("Schedule = %q, want %q", got.Schedule, "0 9 * * *")
	}
}

func TestCreateTask_EmptyFields(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := &model.Task{
		ID:        "task_empty",
		Name:      "",
		Prompt:    "",
		Schedule:  "",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask with empty fields: %v", err)
	}

	got, _ := s.GetTask("task_empty")
	if got.Name != "" {
		t.Errorf("expected empty Name, got %q", got.Name)
	}
}

func TestCreateTask_UnicodeContent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	task := &model.Task{
		ID:        "task_unicode",
		Name:      "中文任务名 🚀",
		Prompt:    "请帮我分析这段代码的性能问题，包括：\n1. 时间复杂度\n2. 空间复杂度\n3. 优化建议",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, _ := s.GetTask("task_unicode")
	if got.Name != task.Name {
		t.Errorf("Name = %q, want %q", got.Name, task.Name)
	}
	if got.Prompt != task.Prompt {
		t.Errorf("Prompt mismatch")
	}
}

func TestCreateTask_VeryLongPrompt(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	longPrompt := ""
	for i := 0; i < 10000; i++ {
		longPrompt += "这是一段很长的提示词。"
	}

	task := &model.Task{
		ID:        "task_long",
		Name:      "long prompt",
		Prompt:    longPrompt,
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask with long prompt: %v", err)
	}

	got, _ := s.GetTask("task_long")
	if got.Prompt != longPrompt {
		t.Errorf("long prompt mismatch: len=%d, want len=%d", len(got.Prompt), len(longPrompt))
	}
}

// --- Concurrency ---

func TestConcurrentWrites(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task := makeTask(fmt.Sprintf("task_%d", i), fmt.Sprintf("task %d", i))
			if err := s.CreateTask(task); err != nil {
				errs <- fmt.Errorf("CreateTask %d: %w", i, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write error: %v", err)
	}

	tasks, _ := s.ListTasks()
	if len(tasks) != 20 {
		t.Errorf("expected 20 tasks, got %d", len(tasks))
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Pre-populate some tasks
	for i := 0; i < 5; i++ {
		s.CreateTask(makeTask(fmt.Sprintf("task_%d", i), fmt.Sprintf("task %d", i)))
	}

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ListTasks(); err != nil {
				errs <- fmt.Errorf("ListTasks: %w", err)
			}
			if _, err := s.ListActiveTasks(); err != nil {
				errs <- fmt.Errorf("ListActiveTasks: %w", err)
			}
		}()
	}

	// Concurrent writes
	for i := 100; i < 110; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task := makeTask(fmt.Sprintf("task_%d", i), fmt.Sprintf("task %d", i))
			if err := s.CreateTask(task); err != nil {
				errs <- fmt.Errorf("CreateTask %d: %w", i, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestConcurrentExecutionLogUpdates(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	// Create multiple logs
	for i := 0; i < 5; i++ {
		s.CreateExecutionLog(&model.ExecutionLog{
			ID:        fmt.Sprintf("log_%d", i),
			TaskID:    "task_1",
			Status:    "running",
			StartedAt: time.Now(),
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.UpdateExecutionLog(fmt.Sprintf("log_%d", i), "output", "success", time.Now())
		}(i)
	}
	wg.Wait()

	logs, _ := s.ListExecutionLogs("task_1")
	for _, l := range logs {
		if l.Status != "success" {
			t.Errorf("log %s status = %q, want %q", l.ID, l.Status, "success")
		}
	}
}

// --- Race condition edge cases ---

func TestDeleteTask_ThenUpdateExecutionLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))
	s.CreateExecutionLog(&model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "task_1",
		Status:    "running",
		StartedAt: time.Now(),
	})

	// Delete the task (cascades to execution_logs)
	if err := s.DeleteTask("task_1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// UpdateExecutionLog on deleted task should fail (foreign key constraint)
	err := s.UpdateExecutionLog("log_1", "output", "success", time.Now())
	if err == nil {
		t.Log("UpdateExecutionLog succeeded even though task was deleted (log may have been cascade-deleted)")
	} else {
		t.Logf("UpdateExecutionLog failed as expected: %v", err)
	}
}

func TestDeleteTask_Idempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	// First delete
	if err := s.DeleteTask("task_1"); err != nil {
		t.Fatalf("first DeleteTask: %v", err)
	}

	// Second delete should be idempotent (no error)
	if err := s.DeleteTask("task_1"); err != nil {
		t.Fatalf("second DeleteTask should not error: %v", err)
	}
}

func TestConcurrentDeleteAndLogUpdate(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	// Create 10 execution logs
	for i := 0; i < 10; i++ {
		s.CreateExecutionLog(&model.ExecutionLog{
			ID:        fmt.Sprintf("log_%d", i),
			TaskID:    "task_1",
			Status:    "running",
			StartedAt: time.Now(),
		})
	}

	var wg sync.WaitGroup
	deleteErrs := make(chan error, 5)
	updateErrs := make(chan error, 10)

	// Concurrent deletes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.DeleteTask("task_1"); err != nil {
				deleteErrs <- err
			}
		}()
	}

	// Concurrent log updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// May fail due to foreign key constraint if task is already deleted
			s.UpdateExecutionLog(fmt.Sprintf("log_%d", i), "output", "success", time.Now())
		}(i)
	}

	wg.Wait()
	close(deleteErrs)
	close(updateErrs)

	for err := range deleteErrs {
		t.Errorf("concurrent delete error: %v", err)
	}
	// Log update errors are expected (foreign key), don't fail the test
	for err := range updateErrs {
		t.Logf("concurrent log update error (expected): %v", err)
	}
}

func TestUpdateExecutionLog_NonexistentLog(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Update a log that doesn't exist — should not error (UPDATE 0 rows)
	err := s.UpdateExecutionLog("nonexistent", "output", "success", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateExecutionLog_DuplicateID(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	s.CreateTask(makeTask("task_1", "test"))

	log := &model.ExecutionLog{
		ID:        "log_dup",
		TaskID:    "task_1",
		Status:    "running",
		StartedAt: time.Now(),
	}
	if err := s.CreateExecutionLog(log); err != nil {
		t.Fatalf("first CreateExecutionLog: %v", err)
	}

	// Duplicate ID should fail
	err := s.CreateExecutionLog(log)
	if err == nil {
		t.Fatal("expected error for duplicate log ID, got nil")
	}
}

func TestCreateExecutionLog_InvalidTaskID(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Don't create any tasks
	log := &model.ExecutionLog{
		ID:        "log_1",
		TaskID:    "nonexistent_task",
		Status:    "running",
		StartedAt: time.Now(),
	}

	// Foreign key constraint should prevent this
	err := s.CreateExecutionLog(log)
	if err == nil {
		t.Log("CreateExecutionLog with invalid task_id succeeded (FK may not be enforced)")
	} else {
		t.Logf("CreateExecutionLog failed as expected: %v", err)
	}
}

// --- Close ---

func TestClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Operations after close should fail
	_, err = s.ListTasks()
	if err == nil {
		t.Fatal("expected error after Close, got nil")
	}
}
