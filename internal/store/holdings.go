package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pauluszhou/bubbles/internal/model"
)

// holdingUpdatable is the whitelist of columns that partial updates may set.
var holdingUpdatable = map[string]bool{
	"name": true, "market": true, "qty": true, "cost": true, "state": true,
	"hypothesis": true, "stop_loss": true, "target": true,
	"review_date": true, "decision": true, "notes": true,
}

// GetHolding returns one holding, or sql.ErrNoRows when unknown.
func (s *Store) GetHolding(ticker string) (*model.Holding, error) {
	row := s.db.QueryRow(`
		SELECT ticker, name, market, qty, cost, state, hypothesis,
		       stop_loss, target, review_date, decision, notes, updated_at
		FROM holdings WHERE ticker = ?`, ticker)
	return scanHolding(row)
}

// ListHoldings returns all holdings ordered by ticker.
func (s *Store) ListHoldings() ([]model.Holding, error) {
	rows, err := s.db.Query(`
		SELECT ticker, name, market, qty, cost, state, hypothesis,
		       stop_loss, target, review_date, decision, notes, updated_at
		FROM holdings ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holdings []model.Holding
	for rows.Next() {
		h, err := scanHoldingRow(rows)
		if err != nil {
			return nil, err
		}
		holdings = append(holdings, *h)
	}
	return holdings, rows.Err()
}

// UpsertHolding inserts or partially updates a holding. Only whitelisted
// columns in fields are applied; omitted fields keep their stored values on
// update and take defaults on insert. Returns created=true for inserts.
func (s *Store) UpsertHolding(ticker string, fields map[string]any) (bool, error) {
	if ticker == "" {
		return false, fmt.Errorf("ticker is required")
	}
	cols := make([]string, 0, len(fields))
	for col := range fields {
		if !holdingUpdatable[col] {
			return false, fmt.Errorf("unknown holding field: %s", col)
		}
		cols = append(cols, col)
	}
	sort.Strings(cols)

	now := time.Now()
	existing, err := s.GetHolding(ticker)
	if err == sql.ErrNoRows {
		// insert: name is mandatory for new rows
		if _, ok := fields["name"]; !ok {
			return false, fmt.Errorf("creating holding %s requires name", ticker)
		}
		insertCols := append([]string{"ticker"}, cols...)
		insertCols = append(insertCols, "updated_at")
		placeholders := make([]string, len(insertCols))
		values := make([]any, len(insertCols))
		placeholders[0] = "?"
		values[0] = ticker
		for i, col := range cols {
			placeholders[i+1] = "?"
			values[i+1] = fields[col]
		}
		placeholders[len(insertCols)-1] = "?"
		values[len(insertCols)-1] = now
		_, err = s.db.Exec(fmt.Sprintf(
			"INSERT INTO holdings (%s) VALUES (%s)",
			strings.Join(insertCols, ", "), strings.Join(placeholders, ", ")),
			values...)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_ = existing

	if len(cols) == 0 {
		return false, nil // nothing to update
	}
	sets := make([]string, 0, len(cols)+1)
	values := make([]any, 0, len(cols)+2)
	for _, col := range cols {
		sets = append(sets, col+" = ?")
		values = append(values, fields[col])
	}
	sets = append(sets, "updated_at = ?")
	values = append(values, now, ticker)
	_, err = s.db.Exec(fmt.Sprintf(
		"UPDATE holdings SET %s WHERE ticker = ?", strings.Join(sets, ", ")),
		values...)
	return false, err
}

// DeleteHolding removes a holding. Returns sql.ErrNoRows when unknown.
func (s *Store) DeleteHolding(ticker string) error {
	res, err := s.db.Exec("DELETE FROM holdings WHERE ticker = ?", ticker)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHolding(row rowScanner) (*model.Holding, error) {
	var (
		h     model.Holding
		qty   sql.NullFloat64
		cost  sql.NullFloat64
		stop  sql.NullFloat64
		tgt   sql.NullFloat64
		updAt time.Time
	)
	err := row.Scan(&h.Ticker, &h.Name, &h.Market, &qty, &cost, &h.State, &h.Hypothesis,
		&stop, &tgt, &h.ReviewDate, &h.Decision, &h.Notes, &updAt)
	if err != nil {
		return nil, err
	}
	h.Qty = nullFloat(qty)
	h.Cost = nullFloat(cost)
	h.StopLoss = nullFloat(stop)
	h.Target = nullFloat(tgt)
	h.UpdatedAt = updAt.Format("2006-01-02 15:04:05")
	return &h, nil
}

func scanHoldingRow(rows *sql.Rows) (*model.Holding, error) {
	return scanHolding(rows)
}

func nullFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}
