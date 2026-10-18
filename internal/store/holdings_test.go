package store

import (
	"database/sql"
	"testing"
)

func TestHoldingsCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// create requires name
	if _, err := s.UpsertHolding("HK.01810", map[string]any{"qty": 15800.0}); err == nil {
		t.Fatal("expected error creating holding without name")
	}

	// create
	created, err := s.UpsertHolding("HK.01810", map[string]any{
		"name": "小米集团", "market": "港股", "qty": 15800.0, "cost": 31.335,
		"state": "holding", "stop_loss": 25.08, "target": 35.0,
	})
	if err != nil || !created {
		t.Fatalf("create failed: created=%v err=%v", created, err)
	}

	h, err := s.GetHolding("HK.01810")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if h.Name != "小米集团" || h.Qty == nil || *h.Qty != 15800 || h.Cost == nil || *h.Cost != 31.335 {
		t.Fatalf("unexpected holding: %+v", h)
	}
	if h.State != "holding" || h.StopLoss == nil || *h.StopLoss != 25.08 {
		t.Fatalf("unexpected holding fields: %+v", h)
	}
	if h.UpdatedAt == "" {
		t.Fatal("updated_at not set")
	}

	// partial update keeps omitted fields
	created, err = s.UpsertHolding("HK.01810", map[string]any{"cost": 30.0, "review_date": "2026-11-30"})
	if err != nil || created {
		t.Fatalf("update failed: created=%v err=%v", created, err)
	}
	h, _ = s.GetHolding("HK.01810")
	if h.Cost == nil || *h.Cost != 30.0 {
		t.Fatalf("cost not updated: %+v", h)
	}
	if h.Name != "小米集团" || h.Qty == nil || *h.Qty != 15800 || h.ReviewDate != "2026-11-30" {
		t.Fatalf("partial update clobbered fields: %+v", h)
	}

	// unknown column rejected
	if _, err := s.UpsertHolding("HK.01810", map[string]any{"bogus": 1}); err == nil {
		t.Fatal("expected error for unknown column")
	}

	// nullable fields stay nil
	if _, err := s.UpsertHolding("603662.SH", map[string]any{"name": "柯力传感", "qty": 700.0}); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	h2, _ := s.GetHolding("603662.SH")
	if h2.Cost != nil || h2.StopLoss != nil {
		t.Fatalf("expected nil numerics: %+v", h2)
	}

	// list orders by ticker
	list, err := s.ListHoldings()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Ticker != "603662.SH" || list[1].Ticker != "HK.01810" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// delete
	if err := s.DeleteHolding("HK.01810"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetHolding("HK.01810"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
	if err := s.DeleteHolding("HK.01810"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows on double delete, got %v", err)
	}
}
