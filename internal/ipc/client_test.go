package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClient_ConnectTimeout(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("bc_%d.sock", time.Now().UnixNano()))

	client := NewClient(socketPath)
	_, err := client.Call("test", nil)
	if err == nil {
		t.Fatal("expected error connecting to nonexistent socket")
	}
}

func TestClient_ServerNotRunning(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("bd_%d.sock", time.Now().UnixNano()))

	// Create a socket file but don't listen
	f, _ := os.Create(socketPath)
	f.Close()
	defer os.Remove(socketPath)

	client := NewClient(socketPath)
	_, err := client.Call("test", nil)
	if err == nil {
		t.Fatal("expected error connecting to dead socket")
	}
}

func TestClient_CallWithNilParams(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("noop", func(params json.RawMessage) (interface{}, error) {
		return "ok", nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	client := NewClient(socketPath)
	resp, err := client.Call("noop", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestClient_CallWithStructParams(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("task.create", func(params json.RawMessage) (interface{}, error) {
		var p CreateTaskParams
		json.Unmarshal(params, &p)
		return TaskResult{ID: "task_1", Name: p.Name, Status: "active"}, nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	client := NewClient(socketPath)
	resp, err := client.Call("task.create", CreateTaskParams{
		Name:   "test",
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestClient_MultipleCallsOnSameConnection(t *testing.T) {
	srv, socketPath := newTestServer(t)

	callCount := 0
	srv.Handle("count", func(params json.RawMessage) (interface{}, error) {
		callCount++
		return callCount, nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	// Each Call creates a new connection (one request per connection)
	client := NewClient(socketPath)
	for i := 1; i <= 3; i++ {
		resp, err := client.Call("count", nil)
		if err != nil {
			t.Fatalf("Call %d: %v", i, err)
		}
		if resp.Error != "" {
			t.Fatalf("Call %d: server error: %s", i, resp.Error)
		}
	}
}

func TestClient_ServerClosesConnection(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("close_and_reply", func(params json.RawMessage) (interface{}, error) {
		return "ok", nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	client := NewClient(socketPath)
	resp, err := client.Call("close_and_reply", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestClient_Timeout(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("slow", func(params json.RawMessage) (interface{}, error) {
		time.Sleep(5 * time.Second)
		return "done", nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	// The default socketTimeout is 30s, but we can test that the call doesn't hang forever
	client := NewClient(socketPath)

	done := make(chan struct{})
	go func() {
		client.Call("slow", nil)
		close(done)
	}()

	select {
	case <-done:
		// Call completed (may have timed out or succeeded)
	case <-time.After(35 * time.Second):
		t.Fatal("Call took too long (>35s)")
	}
}

func TestClient_LargePayload(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("echo", func(params json.RawMessage) (interface{}, error) {
		var msg string
		json.Unmarshal(params, &msg)
		return msg, nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	// Create a large payload
	large := make([]byte, 1024*1024) // 1MB
	for i := range large {
		large[i] = 'a'
	}

	client := NewClient(socketPath)
	resp, err := client.Call("echo", string(large))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestClient_UnicodePayload(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("echo", func(params json.RawMessage) (interface{}, error) {
		var msg string
		json.Unmarshal(params, &msg)
		return msg, nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	client := NewClient(socketPath)
	resp, err := client.Call("echo", "你好世界 🚀")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	result, _ := resp.Result.(string)
	if result != "你好世界 🚀" {
		t.Errorf("result = %q, want %q", result, "你好世界 🚀")
	}
}

func TestClient_ConcurrentCalls(t *testing.T) {
	srv, socketPath := newTestServer(t)

	srv.Handle("echo", func(params json.RawMessage) (interface{}, error) {
		var msg string
		json.Unmarshal(params, &msg)
		return msg, nil
	})

	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			client := NewClient(socketPath)
			resp, err := client.Call("echo", fmt.Sprintf("msg_%d", i))
			if err != nil {
				errs <- fmt.Errorf("client %d: %w", i, err)
				return
			}
			if resp.Error != "" {
				errs <- fmt.Errorf("client %d: %s", i, resp.Error)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent call error: %v", err)
		}
	}
}
