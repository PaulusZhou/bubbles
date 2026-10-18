package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/pauluszhou/bubbles/internal/ipc"
)

func (s *Scheduler) handleHoldingList(json.RawMessage) (interface{}, error) {
	holdings, err := s.store.ListHoldings()
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}
	items := make([]interface{}, 0, len(holdings))
	for i := range holdings {
		items = append(items, holdings[i])
	}
	return ipc.HoldingListResult{Holdings: items, Count: len(items)}, nil
}

func (s *Scheduler) handleHoldingGet(raw json.RawMessage) (interface{}, error) {
	var p ipc.HoldingGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}
	h, err := s.store.GetHolding(p.Ticker)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("holding not found: %s", p.Ticker)
	}
	if err != nil {
		return nil, fmt.Errorf("get holding: %w", err)
	}
	return ipc.HoldingResult{Holding: h}, nil
}

func (s *Scheduler) handleHoldingUpsert(raw json.RawMessage) (interface{}, error) {
	var p ipc.HoldingUpsertParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}

	fields := map[string]any{}
	if p.Name != nil {
		fields["name"] = *p.Name
	}
	if p.Market != nil {
		fields["market"] = *p.Market
	}
	if p.Qty != nil {
		fields["qty"] = *p.Qty
	}
	if p.Cost != nil {
		fields["cost"] = *p.Cost
	}
	if p.State != nil {
		fields["state"] = *p.State
	}
	if p.Hypothesis != nil {
		fields["hypothesis"] = *p.Hypothesis
	}
	if p.StopLoss != nil {
		fields["stop_loss"] = *p.StopLoss
	}
	if p.Target != nil {
		fields["target"] = *p.Target
	}
	if p.ReviewDate != nil {
		fields["review_date"] = *p.ReviewDate
	}
	if p.Decision != nil {
		fields["decision"] = *p.Decision
	}
	if p.Notes != nil {
		fields["notes"] = *p.Notes
	}

	created, err := s.store.UpsertHolding(p.Ticker, fields)
	if err != nil {
		return nil, err
	}
	h, err := s.store.GetHolding(p.Ticker)
	if err != nil {
		return nil, fmt.Errorf("reload holding: %w", err)
	}
	slog.Info("ipc: holding upserted", "ticker", p.Ticker, "created", created, "fields", len(fields))
	return ipc.HoldingResult{Created: created, Holding: h}, nil
}

func (s *Scheduler) handleHoldingDelete(raw json.RawMessage) (interface{}, error) {
	var p ipc.HoldingGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}
	if err := s.store.DeleteHolding(p.Ticker); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("holding not found: %s", p.Ticker)
		}
		return nil, fmt.Errorf("delete holding: %w", err)
	}
	slog.Info("ipc: holding deleted", "ticker", p.Ticker)
	return ipc.HoldingResult{Holding: map[string]string{"ticker": p.Ticker, "deleted": "true"}}, nil
}
