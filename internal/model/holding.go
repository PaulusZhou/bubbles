package model

// Holding is a portfolio position tracked in the holdings table.
// Nullable numeric fields are pointers so partial updates can distinguish
// "unset" from zero.
type Holding struct {
	Ticker     string   `json:"ticker"`
	Name       string   `json:"name"`
	Market     string   `json:"market,omitempty"`
	Qty        *float64 `json:"qty,omitempty"`
	Cost       *float64 `json:"cost,omitempty"`
	State      string   `json:"state,omitempty"`
	Hypothesis string   `json:"hypothesis,omitempty"`
	StopLoss   *float64 `json:"stop_loss,omitempty"`
	Target     *float64 `json:"target,omitempty"`
	ReviewDate string   `json:"review_date,omitempty"`
	Decision   string   `json:"decision,omitempty"`
	Notes      string   `json:"notes,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}
