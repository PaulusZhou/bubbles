package agent

import "context"

// UIRequest is an `extension_ui_request` record emitted by pi's extension UI
// subprotocol in RPC mode (see pi docs: rpc-extension-ui.md). Dialog methods
// (select/confirm/input/editor) expect a response; the others are
// fire-and-forget.
type UIRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`

	// Dialog fields (select, confirm, input, editor).
	Title       string   `json:"title,omitempty"`
	Message     string   `json:"message,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Prefill     string   `json:"prefill,omitempty"`
	Timeout     int64    `json:"timeout,omitempty"` // milliseconds; pi auto-resolves on expiry

	// Fire-and-forget fields (notify, setStatus, setWidget, setTitle, set_editor_text).
	NotifyType      string   `json:"notifyType,omitempty"`
	StatusKey       string   `json:"statusKey,omitempty"`
	StatusText      string   `json:"statusText,omitempty"`
	WidgetKey       string   `json:"widgetKey,omitempty"`
	WidgetLines     []string `json:"widgetLines,omitempty"`
	WidgetPlacement string   `json:"widgetPlacement,omitempty"`
	Text            string   `json:"text,omitempty"`
}

// UIResponse is the `extension_ui_response` sent back for dialog requests.
type UIResponse struct {
	Type      string  `json:"type"` // always "extension_ui_response"
	ID        string  `json:"id"`
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

// NewUIConfirmResponse builds a yes/no answer for a confirm request.
func NewUIConfirmResponse(id string, confirmed bool) *UIResponse {
	return &UIResponse{Type: "extension_ui_response", ID: id, Confirmed: &confirmed}
}

// NewUIValueResponse builds a value answer for select/input/editor requests.
func NewUIValueResponse(id, value string) *UIResponse {
	return &UIResponse{Type: "extension_ui_response", ID: id, Value: &value}
}

// NewUICancelResponse dismisses a dialog (the extension receives undefined or
// false depending on the method).
func NewUICancelResponse(id string) *UIResponse {
	return &UIResponse{Type: "extension_ui_response", ID: id, Cancelled: true}
}

// IsDialog reports whether the request expects an extension_ui_response.
func (r UIRequest) IsDialog() bool {
	switch r.Method {
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// UIBridge forwards pi extension UI requests to a host UI (e.g. Feishu cards).
// Implementations must be safe for concurrent use.
type UIBridge interface {
	// HandleUIRequest displays the request and blocks until it is resolved.
	// For dialog methods it returns the response for pi; nil means cancelled.
	// Fire-and-forget methods return nil and get no response.
	HandleUIRequest(ctx context.Context, req UIRequest) *UIResponse
}
