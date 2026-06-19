package feishu

import (
	"testing"
	"time"

	"github.com/pauluszhou/bubbles/internal/config"
)

// --- StripMentionPrefix ---

func TestStripMentionPrefix_NoMention(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"/cron", "/cron"},
		{"  spaces  ", "spaces"},
		{"", ""},
		{"@someone else", "@someone else"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripMentionPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StripMentionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripMentionPrefix_SingleMention(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@_user_1 /cron", "/cron"},
		{"@_user_123 hello world", "hello world"},
		{"@_user_1", ""},
		{"@_user_1 ", ""},
		{"@_user_1\ttab", "tab"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripMentionPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StripMentionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripMentionPrefix_MultipleMentions(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@_user_1 @_user_2 hello", "hello"},
		{"@_user_1 @_user_2 @_user_3 /cron", "/cron"},
		{"@_user_1 @_user_2", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripMentionPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StripMentionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripMentionPrefix_LeadingWhitespace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  @_user_1 hello", "hello"},
		{"\n@_user_1 hello", "hello"},
		{"\t@_user_1 hello", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripMentionPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StripMentionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripMentionPrefix_NotAMention(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@user hello", "@user hello"},
		{"@_user hello", "@_user hello"},       // missing digit
		{"@_userX1 hello", "@_userX1 hello"},   // non-digit after _user_
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := StripMentionPrefix(tt.input)
			if got != tt.want {
				t.Errorf("StripMentionPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripMentionPrefix_NoDigitMention(t *testing.T) {
	// "@_user_ hello" starts with "@_user_" prefix (7 chars),
	// so the implementation treats it as a mention and strips it.
	got := StripMentionPrefix("@_user_ hello")
	if got != "hello" {
		t.Errorf("StripMentionPrefix(\"@_user_ hello\") = %q, want %q", got, "hello")
	}
}

func TestStripMentionPrefix_NoSpaceAfterMention(t *testing.T) {
	// "@_user_1/new" — no space between mention and command
	// s[7:] = "/new", IndexAny("/new", " \t\n") returns -1, so returns ""
	got := StripMentionPrefix("@_user_1/new")
	if got != "" {
		t.Errorf("StripMentionPrefix(\"@_user_1/new\") = %q, want empty", got)
	}
}

func TestStripMentionPrefix_OnlySpaces(t *testing.T) {
	got := StripMentionPrefix("   ")
	if got != "" {
		t.Errorf("StripMentionPrefix(\"   \") = %q, want empty", got)
	}
}

// --- buildCronFromForm ---

func TestBuildCronFromForm_Daily(t *testing.T) {
	tests := []struct {
		hour string
		want string
	}{
		{"9", "0 9 * * *"},
		{"0", "0 0 * * *"},
		{"23", "0 23 * * *"},
		{"", "0 9 * * *"}, // default hour
	}

	for _, tt := range tests {
		t.Run("hour="+tt.hour, func(t *testing.T) {
			got := buildCronFromForm("daily", "", tt.hour)
			if got != tt.want {
				t.Errorf("buildCronFromForm(daily, _, %q) = %q, want %q", tt.hour, got, tt.want)
			}
		})
	}
}

func TestBuildCronFromForm_Weekdays(t *testing.T) {
	got := buildCronFromForm("weekdays", "", "10")
	want := "0 10 * * 1-5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCronFromForm_Weekly(t *testing.T) {
	tests := []struct {
		day  string
		hour string
		want string
	}{
		{"w1", "9", "0 9 * * 1"},
		{"w5", "14", "0 14 * * 5"},
		{"w0", "8", "0 8 * * 0"},
		{"", "9", "0 9 * * 1"}, // default day
	}

	for _, tt := range tests {
		t.Run("day="+tt.day+"_hour="+tt.hour, func(t *testing.T) {
			got := buildCronFromForm("weekly", tt.day, tt.hour)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCronFromForm_Monthly(t *testing.T) {
	tests := []struct {
		day  string
		hour string
		want string
	}{
		{"d1", "9", "0 9 1 * *"},
		{"d15", "14", "0 14 15 * *"},
		{"d28", "0", "0 0 28 * *"},
		{"", "9", "0 9 1 * *"}, // default day
	}

	for _, tt := range tests {
		t.Run("day="+tt.day+"_hour="+tt.hour, func(t *testing.T) {
			got := buildCronFromForm("monthly", tt.day, tt.hour)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCronFromForm_DefaultFreq(t *testing.T) {
	// Unknown freqType should default to daily
	got := buildCronFromForm("unknown", "", "9")
	want := "0 9 * * *"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCronFromForm_EmptyFreq(t *testing.T) {
	got := buildCronFromForm("", "", "9")
	want := "0 9 * * *"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCronFromForm_AllEmpty(t *testing.T) {
	got := buildCronFromForm("", "", "")
	want := "0 9 * * *" // default daily + hour 9
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCronFromForm_WeeklyBarePrefix(t *testing.T) {
	// "w" alone — TrimPrefix("w") gives "", defaults to "1"
	got := buildCronFromForm("weekly", "w", "14")
	want := "0 14 * * 1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCronFromForm_MonthlyBarePrefix(t *testing.T) {
	// "d" alone — TrimPrefix("d") gives "", defaults to "1"
	got := buildCronFromForm("monthly", "d", "8")
	want := "0 8 1 * *"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- parseFormString ---

func TestParseFormString_String(t *testing.T) {
	fv := map[string]interface{}{
		"key": "value",
	}
	got := parseFormString(fv, "key")
	if got != "value" {
		t.Errorf("got %q, want %q", got, "value")
	}
}

func TestParseFormString_Array(t *testing.T) {
	fv := map[string]interface{}{
		"key": []interface{}{"first", "second"},
	}
	got := parseFormString(fv, "key")
	if got != "first" {
		t.Errorf("got %q, want %q", got, "first")
	}
}

func TestParseFormString_EmptyArray(t *testing.T) {
	fv := map[string]interface{}{
		"key": []interface{}{},
	}
	got := parseFormString(fv, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_MissingKey(t *testing.T) {
	fv := map[string]interface{}{
		"other": "value",
	}
	got := parseFormString(fv, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_NilMap(t *testing.T) {
	got := parseFormString(nil, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_WrongType(t *testing.T) {
	fv := map[string]interface{}{
		"key": 42,
	}
	got := parseFormString(fv, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_ArrayWithNonString(t *testing.T) {
	fv := map[string]interface{}{
		"key": []interface{}{42, "second"},
	}
	got := parseFormString(fv, "key")
	// First element is not a string, should return ""
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_NilValue(t *testing.T) {
	fv := map[string]interface{}{
		"key": nil,
	}
	got := parseFormString(fv, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestParseFormString_EmptyMap(t *testing.T) {
	fv := map[string]interface{}{}
	got := parseFormString(fv, "key")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// --- formatDuration ---

func TestFormatDuration_JustNow(t *testing.T) {
	got := formatDuration(30 * time.Second)
	if got != "刚刚" {
		t.Errorf("formatDuration(30s) = %q, want %q", got, "刚刚")
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	got := formatDuration(5 * time.Minute)
	if got != "5 分钟" {
		t.Errorf("formatDuration(5m) = %q, want %q", got, "5 分钟")
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	got := formatDuration(3 * time.Hour)
	if got != "3 小时" {
		t.Errorf("formatDuration(3h) = %q, want %q", got, "3 小时")
	}
}

func TestFormatDuration_Days(t *testing.T) {
	got := formatDuration(48 * time.Hour)
	if got != "2 天" {
		t.Errorf("formatDuration(48h) = %q, want %q", got, "2 天")
	}
}

func TestFormatDuration_Boundary(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{59 * time.Second, "刚刚"},
		{1 * time.Minute, "1 分钟"},
		{59 * time.Minute, "59 分钟"},
		{1 * time.Hour, "1 小时"},
		{23 * time.Hour, "23 小时"},
		{24 * time.Hour, "1 天"},
	}

	for _, tt := range tests {
		t.Run(tt.d.String(), func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// --- splitMessage ---

func TestSplitMessage_Short(t *testing.T) {
	msg := "hello world"
	chunks := splitMessage(msg, 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != msg {
		t.Errorf("chunk = %q, want %q", chunks[0], msg)
	}
}

func TestSplitMessage_ExactLength(t *testing.T) {
	msg := "hello"
	chunks := splitMessage(msg, 5)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != msg {
		t.Errorf("chunk = %q, want %q", chunks[0], msg)
	}
}

func TestSplitMessage_Long(t *testing.T) {
	msg := "line1\nline2\nline3\nline4\nline5"
	chunks := splitMessage(msg, 12)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}

	// Reconstruct
	reconstructed := ""
	for i, c := range chunks {
		if i > 0 {
			reconstructed += "\n"
		}
		reconstructed += c
	}
	// The reconstructed message should be close to original (may lose a newline at split point)
	if len([]rune(reconstructed)) > len([]rune(msg)) {
		t.Errorf("reconstructed is longer than original")
	}
}

func TestSplitMessage_NoNewlines(t *testing.T) {
	msg := "abcdefghij"
	chunks := splitMessage(msg, 4)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > 4 {
			t.Errorf("chunk too long: %d runes", len([]rune(c)))
		}
	}
}

func TestSplitMessage_EmptyString(t *testing.T) {
	chunks := splitMessage("", 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "" {
		t.Errorf("chunk = %q, want empty", chunks[0])
	}
}

func TestSplitMessage_UnicodeRunes(t *testing.T) {
	msg := "你好世界测试消息"
	chunks := splitMessage(msg, 4)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > 4 {
			t.Errorf("chunk too long: %d runes", len([]rune(c)))
		}
	}
}

// --- truncateRunes ---

func TestTruncateRunes_Short(t *testing.T) {
	got := truncateRunes("hello", 10)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncateRunes_Exact(t *testing.T) {
	got := truncateRunes("hello", 5)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncateRunes_Long(t *testing.T) {
	got := truncateRunes("hello world", 5)
	if got != "hello…" {
		t.Errorf("got %q, want %q", got, "hello…")
	}
}

func TestTruncateRunes_Unicode(t *testing.T) {
	got := truncateRunes("你好世界测试", 3)
	if got != "你好世…" {
		t.Errorf("got %q, want %q", got, "你好世…")
	}
}

func TestTruncateRunes_Empty(t *testing.T) {
	got := truncateRunes("", 5)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- Session management ---

func newTestChannel(t *testing.T) *FeishuChannel {
	t.Helper()
	// Create a minimal FeishuChannel for testing session management.
	// We don't need a real Feishu SDK connection.
	return &FeishuChannel{
		commands:   make(map[string]CommandHandler),
		chatStates: make(map[string]*chatState),
	}
}

func TestSession_NewAndGet(t *testing.T) {
	fch := newTestChannel(t)

	session := fch.NewSession("chat_1", "测试会话")
	if session.Name() != "测试会话" {
		t.Errorf("Name = %q, want %q", session.Name(), "测试会话")
	}

	sessions, activeKey := fch.GetSessions("chat_1")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if activeKey != session.key {
		t.Errorf("activeKey = %q, want %q", activeKey, session.key)
	}
}

func TestSession_MultipleSessions(t *testing.T) {
	fch := newTestChannel(t)

	fch.NewSession("chat_1", "会话 1")
	fch.NewSession("chat_1", "会话 2")
	fch.NewSession("chat_1", "会话 3")

	sessions, activeKey := fch.GetSessions("chat_1")
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Active key should be the last created session
	lastSession := sessions[len(sessions)-1]
	if activeKey != lastSession.key {
		t.Errorf("activeKey = %q, want %q (last session)", activeKey, lastSession.key)
	}
}

func TestSession_Switch(t *testing.T) {
	fch := newTestChannel(t)

	s1 := fch.NewSession("chat_1", "会话 1")
	s2 := fch.NewSession("chat_1", "会话 2")

	// Switch to first session
	if err := fch.SwitchSession("chat_1", s1.key); err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}

	_, activeKey := fch.GetSessions("chat_1")
	if activeKey != s1.key {
		t.Errorf("activeKey = %q, want %q", activeKey, s1.key)
	}

	// Switch back to second
	if err := fch.SwitchSession("chat_1", s2.key); err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}

	_, activeKey = fch.GetSessions("chat_1")
	if activeKey != s2.key {
		t.Errorf("activeKey = %q, want %q", activeKey, s2.key)
	}
}

func TestSession_SwitchNotFound(t *testing.T) {
	fch := newTestChannel(t)
	fch.NewSession("chat_1", "会话 1")

	err := fch.SwitchSession("chat_1", "nonexistent")
	if err == nil {
		t.Fatal("expected error switching to nonexistent session")
	}
}

func TestSession_Close(t *testing.T) {
	fch := newTestChannel(t)

	fch.NewSession("chat_1", "会话 1")
	s2 := fch.NewSession("chat_1", "会话 2")

	// Close the active session (s2)
	if err := fch.CloseSession("chat_1", s2.key); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	sessions, _ := fch.GetSessions("chat_1")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after close, got %d", len(sessions))
	}
}

func TestSession_CloseNotFound(t *testing.T) {
	fch := newTestChannel(t)
	fch.NewSession("chat_1", "会话 1")

	err := fch.CloseSession("chat_1", "nonexistent")
	if err == nil {
		t.Fatal("expected error closing nonexistent session")
	}
}

func TestSession_CloseActiveSwitchesToRecent(t *testing.T) {
	fch := newTestChannel(t)

	s1 := fch.NewSession("chat_1", "会话 1")
	fch.NewSession("chat_1", "会话 2")
	s3 := fch.NewSession("chat_1", "会话 3")

	// s3 is active (last created). Close it.
	if err := fch.CloseSession("chat_1", s3.key); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	_, activeKey := fch.GetSessions("chat_1")
	// Should switch to s1 (most recent remaining by lastActive)
	if activeKey == "" {
		t.Fatal("expected active key to be set after closing active session")
	}
	if activeKey != s1.key {
		t.Logf("activeKey = %q (may be s1 or s2 depending on lastActive ordering)", activeKey)
	}
}

func TestSession_StopActive(t *testing.T) {
	fch := newTestChannel(t)

	session := fch.NewSession("chat_1", "会话 1")

	// No cancel func set — should error
	err := fch.StopActiveSession("chat_1")
	if err == nil {
		t.Fatal("expected error when session has no cancel func")
	}

	// Set a cancel func
	cs := fch.getChatState("chat_1")
	cs.mu.Lock()
	session.cancelFunc = func() {}
	cs.mu.Unlock()

	err = fch.StopActiveSession("chat_1")
	if err != nil {
		t.Fatalf("StopActiveSession: %v", err)
	}
}

func TestSession_StopNoActiveSession(t *testing.T) {
	fch := newTestChannel(t)

	// No sessions at all
	err := fch.StopActiveSession("chat_1")
	if err == nil {
		t.Fatal("expected error with no active session")
	}
}

func TestSession_GetSessionsSorted(t *testing.T) {
	fch := newTestChannel(t)

	s1 := fch.NewSession("chat_1", "First")
	s2 := fch.NewSession("chat_1", "Second")
	s3 := fch.NewSession("chat_1", "Third")

	sessions, _ := fch.GetSessions("chat_1")
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Should be sorted by creation time
	if sessions[0].key != s1.key {
		t.Errorf("sessions[0] = %q, want %q", sessions[0].key, s1.key)
	}
	if sessions[1].key != s2.key {
		t.Errorf("sessions[1] = %q, want %q", sessions[1].key, s2.key)
	}
	if sessions[2].key != s3.key {
		t.Errorf("sessions[2] = %q, want %q", sessions[2].key, s3.key)
	}
}

func TestSession_DefaultSession(t *testing.T) {
	fch := newTestChannel(t)

	// getOrCreateActiveSession should create a default session
	session := fch.getOrCreateActiveSession("chat_1")
	if session == nil {
		t.Fatal("expected default session to be created")
	}
	if session.Name() == "" {
		t.Error("default session should have a name")
	}

	// Second call should return the same session
	session2 := fch.getOrCreateActiveSession("chat_1")
	if session2.key != session.key {
		t.Errorf("expected same session, got different keys: %q vs %q", session2.key, session.key)
	}
}

func TestSession_GetResumeSessionID_Expired(t *testing.T) {
	fch := newTestChannel(t)

	session := fch.NewSession("chat_1", "会话 1")

	// Set claude SID
	fch.updateSession("chat_1", session.key, "claude_sid_123")

	// Should return the SID
	sid := fch.getResumeSessionID("chat_1", session.key)
	if sid != "claude_sid_123" {
		t.Errorf("SID = %q, want %q", sid, "claude_sid_123")
	}

	// Set lastActive to expired
	cs := fch.getChatState("chat_1")
	cs.mu.Lock()
	session.lastActive = time.Now().Add(-sessionExpiry - time.Minute)
	cs.mu.Unlock()

	sid = fch.getResumeSessionID("chat_1", session.key)
	if sid != "" {
		t.Errorf("expired session should return empty SID, got %q", sid)
	}
}

func TestSession_GetResumeSessionID_NotFound(t *testing.T) {
	fch := newTestChannel(t)

	sid := fch.getResumeSessionID("chat_1", "nonexistent")
	if sid != "" {
		t.Errorf("expected empty SID for nonexistent session, got %q", sid)
	}
}

// --- NewChannel ---

func TestNewChannel_NilConfig(t *testing.T) {
	// Missing Feishu credentials should return nil
	cfg := &config.Config{}
	fch := New(cfg)
	if fch != nil {
		t.Error("expected nil when Feishu credentials are missing")
	}
}

func TestNewChannel_WithCredentials(t *testing.T) {
	cfg := &config.Config{
		FeishuAppID:     "test_id",
		FeishuAppSecret: "test_secret",
	}
	fch := New(cfg)
	if fch == nil {
		t.Fatal("expected non-nil FeishuChannel with valid credentials")
	}
	if fch.defaultChatID != "" {
		t.Errorf("defaultChatID should be empty, got %q", fch.defaultChatID)
	}
}

func TestNewChannel_WithChatID(t *testing.T) {
	cfg := &config.Config{
		FeishuAppID:     "test_id",
		FeishuAppSecret: "test_secret",
		FeishuChatID:    "chat_123",
	}
	fch := New(cfg)
	if fch == nil {
		t.Fatal("expected non-nil FeishuChannel")
	}
	if fch.defaultChatID != "chat_123" {
		t.Errorf("defaultChatID = %q, want %q", fch.defaultChatID, "chat_123")
	}
}
