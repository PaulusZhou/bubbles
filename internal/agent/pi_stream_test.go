package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collectStreamEvents runs PiStreamWithEvents against a fake pi script and
// returns the emitted events.
func collectStreamEvents(t *testing.T, scriptBody string, resumeSID string) ([]StreamEvent, error) {
	t.Helper()
	script := writeFakePi(t, scriptBody)

	var events []StreamEvent
	err := PiStreamWithEvents(context.Background(), script, "prompt", t.TempDir(), resumeSID, "", func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	})
	return events, err
}

func TestPiStreamWithEvents_MapsEvents(t *testing.T) {
	events, err := collectStreamEvents(t, `cat <<'EOF'
{"type":"session","version":3,"id":"sess-123","timestamp":"2024-01-01T00:00:00.000Z","cwd":"/tmp"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}
{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"思考中"}}
{"type":"message_update","assistantMessageEvent":{"type":"thinking_end","contentIndex":0,"content":"思考完成"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"最终"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"回答"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"最终回答"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"最终回答"}],"stopReason":"stop"}}
{"type":"agent_settled","aborted":false}
EOF`, "")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []StreamEvent{
		{Type: "system", SessionID: "sess-123"},
		{Type: "thinking", Text: "思考完成"},
		{Type: "text", Text: "最终回答"},
		{Type: "result", Text: "最终回答"},
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events %+v, want %d", len(events), events, len(want))
	}
	for i, w := range want {
		if events[i].Type != w.Type || events[i].Text != w.Text || events[i].SessionID != w.SessionID {
			t.Errorf("event[%d] = %+v, want %+v", i, events[i], w)
		}
	}
}

func TestPiStreamWithEvents_ErrorStopReason(t *testing.T) {
	events, err := collectStreamEvents(t, `cat <<'EOF'
{"type":"session","version":3,"id":"sess-err"}
{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}
{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}}
{"type":"agent_settled","aborted":false}
EOF`, "")

	if err == nil {
		t.Fatal("expected error for stopReason=error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should contain the provider error message", err)
	}

	// The failure must surface as a result event so the card shows why it failed.
	var result *StreamEvent
	for i := range events {
		if events[i].Type == "result" {
			result = &events[i]
		}
	}
	if result == nil {
		t.Fatal("expected a result event")
	}
	if !result.IsError || !strings.Contains(result.Text, "boom") {
		t.Errorf("result event = %+v, want error result containing %q", result, "boom")
	}
}

func TestPiStreamWithEvents_PrematureEnd(t *testing.T) {
	_, err := collectStreamEvents(t, "echo '{\"type\":\"agent_start\"}'\n", "")
	if err == nil {
		t.Fatal("expected error when stream ends before agent_settled")
	}
}

func TestPiStreamWithEvents_CallbackError(t *testing.T) {
	script := writeFakePi(t, `cat <<'EOF'
{"type":"session","version":3,"id":"sess-cb"}
{"type":"agent_settled","aborted":false}
EOF`)

	err := PiStreamWithEvents(context.Background(), script, "prompt", t.TempDir(), "", "", func(ev StreamEvent) error {
		return context.Canceled
	})
	if err == nil {
		t.Fatal("expected error when the callback fails")
	}
}

func TestPiStreamWithEvents_ResumeArgs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("BUBBLES_TEST_ARGS_FILE", argsFile)

	events, err := collectStreamEvents(t, `printf '%s\n' "$@" > "$BUBBLES_TEST_ARGS_FILE"
echo '{"type":"session","version":3,"id":"sess-args"}'
echo '{"type":"agent_settled","aborted":false}'`, "sess-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Type != "system" || events[0].SessionID != "sess-args" {
		t.Errorf("events = %+v, want a single system event", events)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	args := strings.Join(strings.Split(strings.TrimSpace(string(data)), "\n"), " ")
	want := "--mode json --approve --session sess-123 -- prompt"
	if args != want {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestJoinBlocks(t *testing.T) {
	got := joinBlocks(map[int]string{2: "b", 0: "a", 1: ""})
	if got != "a\nb" {
		t.Errorf("joinBlocks = %q, want %q", got, "a\nb")
	}
	if got := joinBlocks(map[int]string{}); got != "" {
		t.Errorf("joinBlocks(empty) = %q, want empty", got)
	}
}

func TestPiTextOf(t *testing.T) {
	if got := piTextOf([]byte(`"plain"`)); got != "plain" {
		t.Errorf("piTextOf(string) = %q", got)
	}
	if got := piTextOf([]byte(`[{"type":"thinking","thinking":"x"},{"type":"text","text":"a"},{"type":"text","text":"b"}]`)); got != "a\nb" {
		t.Errorf("piTextOf(blocks) = %q", got)
	}
}
