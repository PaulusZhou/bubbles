package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPiSessionStats(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats, err := PiSessionStats(ctx, script, t.TempDir(), "", "sess-rpc-1")
	if err != nil {
		t.Fatalf("PiSessionStats: %v", err)
	}
	if !strings.Contains(string(stats), "\"total\":42") {
		t.Errorf("stats = %s, want token total 42", stats)
	}
}

func TestPiSessionExportHTML(t *testing.T) {
	script := writeFakePi(t, fakeRPCPiBody)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path, err := PiSessionExportHTML(ctx, script, t.TempDir(), "", "sess-rpc-1", "/tmp/out.html")
	if err != nil {
		t.Fatalf("PiSessionExportHTML: %v", err)
	}
	if path != "/tmp/session.html" {
		t.Errorf("export path = %q, want /tmp/session.html", path)
	}
}

func TestPiSessionOps_RequireSessionID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := PiSessionStats(ctx, "pi", "", "", ""); err == nil {
		t.Error("PiSessionStats with empty session id should fail")
	}
	if _, err := PiSessionExportHTML(ctx, "pi", "", "", "", ""); err == nil {
		t.Error("PiSessionExportHTML with empty session id should fail")
	}
}
