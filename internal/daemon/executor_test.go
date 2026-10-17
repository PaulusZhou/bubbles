package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/store"
)

// mockRunner implements Runner for testing.
type mockRunner struct {
	mu     sync.Mutex
	result agent.Result
	calls  int
}

func (r *mockRunner) Run(prompt, workDir string) agent.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.result
}

func (r *mockRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// mockFeishuNotifier implements FeishuNotifier for testing.
type mockFeishuNotifier struct {
	mu          sync.Mutex
	completions []TaskCompletion
	texts       []string
	returnErr   error
}

func (n *mockFeishuNotifier) NotifyTaskCompletion(ctx context.Context, completion TaskCompletion) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.completions = append(n.completions, completion)
	return n.returnErr
}

func (n *mockFeishuNotifier) NotifyText(ctx context.Context, chatID, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.texts = append(n.texts, text)
	return n.returnErr
}

// completionList returns a snapshot of recorded completions.
func (n *mockFeishuNotifier) completionList() []TaskCompletion {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]TaskCompletion(nil), n.completions...)
}

// textList returns a snapshot of recorded texts.
func (n *mockFeishuNotifier) textList() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.texts...)
}

func newTestExecutor(t *testing.T) (*Executor, *store.Store, *mockRunner, func()) {
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
	runner := &mockRunner{
		result: agent.Result{Output: "test output"},
	}
	executor := &Executor{
		store:     s,
		agentName: "Claude Code",
		workDir:   cfg.WorkDir,
		runner:    runner,
	}
	cleanup := func() { s.Close() }
	return executor, s, runner, cleanup
}

func waitForAsync(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// --- Tests ---

func TestExecutor_Run_Success(t *testing.T) {
	executor, s, runner, cleanup := newTestExecutor(t)
	defer cleanup()

	s.CreateTask(makeTestTask("task_1", "test task"))

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async goroutine to complete
	waitForAsync(t, 5*time.Second, func() bool {
		log, _ := s.GetLastExecutionLog("task_1")
		return log != nil && log.Status == "success"
	})

	// Verify runner was called (after async completion)
	if runner.callCount() != 1 {
		t.Errorf("expected 1 runner call, got %d", runner.callCount())
	}

	// Verify execution log
	log, _ := s.GetLastExecutionLog("task_1")
	if log == nil {
		t.Fatal("expected execution log")
	}
	if log.Status != "success" {
		t.Errorf("status = %q, want %q", log.Status, "success")
	}
	if log.Output != "test output" {
		t.Errorf("output = %q, want %q", log.Output, "test output")
	}

	// Verify last_run_at was updated
	task, _ := s.GetTask("task_1")
	if task.LastRunAt.IsZero() {
		t.Error("expected LastRunAt to be set")
	}
}

func TestExecutor_Run_AgentFailed(t *testing.T) {
	executor, s, runner, cleanup := newTestExecutor(t)
	defer cleanup()

	runner.result = agent.Result{
		Output: "partial output",
		Error:  fmt.Errorf("claude crashed"),
	}

	s.CreateTask(makeTestTask("task_1", "test task"))

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async goroutine to complete
	waitForAsync(t, 5*time.Second, func() bool {
		log, _ := s.GetLastExecutionLog("task_1")
		return log != nil && log.Status == "failed"
	})

	log, _ := s.GetLastExecutionLog("task_1")
	if log.Status != "failed" {
		t.Errorf("status = %q, want %q", log.Status, "failed")
	}
	if log.Output == "" {
		t.Error("expected non-empty output even on failure")
	}
}

func TestExecutor_Run_TaskNotFound(t *testing.T) {
	executor, _, _, cleanup := newTestExecutor(t)
	defer cleanup()

	err := executor.Run("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestExecutor_Run_OneTimeTaskCleanup(t *testing.T) {
	executor, s, _, cleanup := newTestExecutor(t)
	defer cleanup()

	// Create a one-time task that's already marked as "done" by scheduler
	task := makeTestTask("task_1", "one-time")
	task.Status = "done"
	s.CreateTask(task)

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async goroutine to complete and delete the task
	waitForAsync(t, 5*time.Second, func() bool {
		_, err := s.GetTask("task_1")
		return err != nil // task should be deleted
	})

	// Verify task was deleted
	_, err = s.GetTask("task_1")
	if err == nil {
		t.Error("expected one-time task to be deleted after execution")
	}
}

func TestExecutor_Run_WithFeishuNotifier(t *testing.T) {
	executor, s, _, cleanup := newTestExecutor(t)
	defer cleanup()

	notifier := &mockFeishuNotifier{}
	executor.SetFeishuNotifier(notifier)

	s.CreateTask(makeTestTask("task_1", "test task"))

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async goroutine to complete
	waitForAsync(t, 5*time.Second, func() bool {
		return len(notifier.completionList()) > 0
	})

	// Verify notification was sent
	completions := notifier.completionList()
	if len(completions) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(completions))
	}
	completion := completions[0]
	if completion.TaskID != "task_1" {
		t.Errorf("TaskID = %q, want %q", completion.TaskID, "task_1")
	}
	if completion.TaskName != "test task" {
		t.Errorf("TaskName = %q, want %q", completion.TaskName, "test task")
	}
	if completion.Status != "success" {
		t.Errorf("Status = %q, want %q", completion.Status, "success")
	}
	if completion.Output != "test output" {
		t.Errorf("Output = %q, want %q", completion.Output, "test output")
	}
}

func TestExecutor_Run_FeishuNotifierError(t *testing.T) {
	executor, s, _, cleanup := newTestExecutor(t)
	defer cleanup()

	notifier := &mockFeishuNotifier{returnErr: fmt.Errorf("send failed")}
	executor.SetFeishuNotifier(notifier)

	s.CreateTask(makeTestTask("task_1", "test task"))

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async goroutine to complete
	waitForAsync(t, 5*time.Second, func() bool {
		return len(notifier.completionList()) > 0
	})

	// Notification was attempted even though it failed
	if len(notifier.completionList()) != 1 {
		t.Fatalf("expected 1 notification attempt, got %d", len(notifier.completionList()))
	}
}

func TestExecutor_Run_Concurrent(t *testing.T) {
	executor, s, runner, cleanup := newTestExecutor(t)
	defer cleanup()

	// Create multiple tasks
	for i := 0; i < 5; i++ {
		s.CreateTask(makeTestTask(fmt.Sprintf("task_%d", i), fmt.Sprintf("task %d", i)))
	}

	// Run all tasks concurrently
	for i := 0; i < 5; i++ {
		err := executor.Run(fmt.Sprintf("task_%d", i))
		if err != nil {
			t.Fatalf("Run task_%d: %v", i, err)
		}
	}

	// Wait for all to complete
	waitForAsync(t, 10*time.Second, func() bool {
		return runner.callCount() == 5
	})

	// Verify all execution logs
	for i := 0; i < 5; i++ {
		log, _ := s.GetLastExecutionLog(fmt.Sprintf("task_%d", i))
		if log == nil {
			t.Errorf("expected execution log for task_%d", i)
		} else if log.Status != "success" {
			t.Errorf("task_%d status = %q, want %q", i, log.Status, "success")
		}
	}
}

func TestExecutor_Run_NoNotifier(t *testing.T) {
	executor, s, _, cleanup := newTestExecutor(t)
	defer cleanup()

	// No notifier set — should not panic
	s.CreateTask(makeTestTask("task_1", "test"))

	err := executor.Run("task_1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async completion
	waitForAsync(t, 5*time.Second, func() bool {
		log, _ := s.GetLastExecutionLog("task_1")
		return log != nil && log.Status == "success"
	})
}

func TestExecutor_SetFeishuNotifier(t *testing.T) {
	executor, _, _, cleanup := newTestExecutor(t)
	defer cleanup()

	if executor.feishuNotifier != nil {
		t.Error("expected nil notifier initially")
	}

	notifier := &mockFeishuNotifier{}
	executor.SetFeishuNotifier(notifier)

	if executor.feishuNotifier != notifier {
		t.Error("notifier not set correctly")
	}
}
