package agent

import (
	"context"
	"testing"
	"time"
)

func TestRPCManager_GetReusesSession(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s1, err := m.Get(ctx, "k1", "resume-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	s2, err := m.Get(ctx, "k1", "resume-2")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if s1 != s2 {
		t.Error("Get should reuse the live session for the same key")
	}

	other, err := m.Get(ctx, "k2", "")
	if err != nil {
		t.Fatalf("Get other key: %v", err)
	}
	if other == s1 {
		t.Error("different keys should get different sessions")
	}

	m.CloseAll()
}

func TestRPCManager_Peek(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", time.Minute)

	if s := m.Peek("k1"); s != nil {
		t.Error("Peek on a missing key should return nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := m.Get(ctx, "k1", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s := m.Peek("k1"); s != created {
		t.Error("Peek should return the live session")
	}
	m.Close("k1")
	if s := m.Peek("k1"); s != nil {
		t.Error("Peek after Close should return nil")
	}
}

func TestRPCManager_ReplacesDeadSession(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s1, err := m.Get(ctx, "k1", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = s1.Close()
	<-s1.Done()

	s2, err := m.Get(ctx, "k1", "")
	if err != nil {
		t.Fatalf("Get after death: %v", err)
	}
	if s2 == s1 {
		t.Error("Get should start a fresh session after the process died")
	}
	m.CloseAll()
}

func TestRPCManager_ReapIdle(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", 10*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.Get(ctx, "k1", ""); err != nil {
		t.Fatalf("Get: %v", err)
	}

	m.Reap(time.Now()) // just created: not idle yet
	if m.Peek("k1") == nil {
		t.Error("fresh session should not be reaped")
	}

	m.Reap(time.Now().Add(30 * time.Minute))
	if m.Peek("k1") != nil {
		t.Error("idle session should be reaped")
	}
}

func TestRPCManager_ReapSkipsRunningSession(t *testing.T) {
	t.Setenv("FAKE_PI_DEFER_SETTLE", "1")
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", 10*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := m.Get(ctx, "k1", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	textSeen := make(chan struct{})
	var once bool
	runDone := make(chan error, 1)
	go func() {
		runDone <- s.RunPrompt(ctx, "hi", func(ev StreamEvent) error {
			if ev.Type == "text" && !once {
				once = true
				close(textSeen)
			}
			return nil
		})
	}()
	select {
	case <-textSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for text event")
	}

	m.Reap(time.Now().Add(30 * time.Minute))
	if m.Peek("k1") == nil {
		t.Error("running session must not be reaped")
	}

	if err := s.Steer(ctx, "wrap up"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not settle")
	}
	m.CloseAll()
}

func TestRPCManager_RunJanitorClosesOnCancel(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	m := NewRPCManager(script, t.TempDir(), "", 10*time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.RunJanitor(ctx, 10*time.Millisecond)
		close(done)
	}()

	getCtx, getCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer getCancel()
	s, err := m.Get(getCtx, "k1", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("janitor did not stop")
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("janitor should close all sessions on shutdown")
	}
}
