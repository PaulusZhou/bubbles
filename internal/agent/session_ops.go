package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// withRPCSession runs fn against a short-lived pi RPC process bound to the
// given session. Used for out-of-band session operations (stats, export) that
// do not need a long-lived chat session.
func withRPCSession(ctx context.Context, piPath, workDir, model, sessionID string, fn func(*RPCSession) error) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	s := NewRPCSession(piPath, workDir, model, sessionID)
	if err := s.Start(ctx); err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

// PiSessionStats returns the session statistics record (tokens, cost, context
// usage) for a pi session.
func PiSessionStats(ctx context.Context, piPath, workDir, model, sessionID string) (json.RawMessage, error) {
	var stats json.RawMessage
	err := withRPCSession(ctx, piPath, workDir, model, sessionID, func(s *RPCSession) error {
		var err error
		stats, err = s.GetSessionStats(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("session stats %s: %w", sessionID, err)
	}
	return stats, nil
}

// PiSessionExportHTML exports a pi session to an HTML file. outPath may be
// empty, in which case pi picks its default location. The path actually
// written is returned.
func PiSessionExportHTML(ctx context.Context, piPath, workDir, model, sessionID, outPath string) (string, error) {
	var written string
	err := withRPCSession(ctx, piPath, workDir, model, sessionID, func(s *RPCSession) error {
		var err error
		written, err = s.ExportHTML(ctx, outPath)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("session export %s: %w", sessionID, err)
	}
	return written, nil
}
