package feishu

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pauluszhou/bubbles/internal/daemon"
)

// --- Button helpers ---

// taskButtonRow creates a v2 card column_set with action buttons.
// v2 cards don't support the "action" tag; buttons must be inside column_set -> column.
func taskButtonRow(buttons ...map[string]interface{}) map[string]interface{} {
	columns := make([]map[string]interface{}, len(buttons))
	for i, btn := range buttons {
		columns[i] = map[string]interface{}{
			"tag":      "column",
			"width":    "auto",
			"elements": []map[string]interface{}{btn},
		}
	}
	return map[string]interface{}{
		"tag":     "column_set",
		"columns": columns,
	}
}

// taskButton creates a v2 card button with value for SDK callback compatibility.
func taskButton(text, buttonType, action, taskID string) map[string]interface{} {
	value := map[string]string{"action": action, "task_id": taskID}
	return map[string]interface{}{
		"tag":       "button",
		"text":      map[string]string{"tag": "plain_text", "content": text},
		"type":      buttonType,
		"value":     value,
		"behaviors": []map[string]interface{}{{"type": "callback", "value": value}},
	}
}

// --- Task list card ---

// BuildTaskCardJSON builds a Feishu v2 interactive card from a list of task summaries.
// Each task row has action buttons appropriate to its type (cron vs one-time).
func BuildTaskCardJSON(summaries []daemon.TaskSummary) (string, error) {
	if len(summaries) == 0 {
		card := map[string]interface{}{
			"schema": "2.0",
			"header": map[string]interface{}{
				"template": "blue",
				"title":    map[string]interface{}{"tag": "plain_text", "content": "📋 任务列表"},
			},
			"body": map[string]interface{}{
				"elements": []map[string]interface{}{
					{"tag": "markdown", "content": "当前没有活跃任务"},
				},
			},
		}
		bs, err := json.Marshal(card)
		return string(bs), err
	}

	var cronTasks, oneTimeTasks []daemon.TaskSummary
	for _, t := range summaries {
		if t.Schedule != "" {
			cronTasks = append(cronTasks, t)
		} else {
			oneTimeTasks = append(oneTimeTasks, t)
		}
	}

	var elements []map[string]interface{}

	if len(cronTasks) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag": "markdown", "content": "⏰ **定时任务**",
		})
		for _, t := range cronTasks {
			nextRun := t.NextRunAt
			if nextRun == "" {
				nextRun = "-"
			}
			row := fmt.Sprintf("**%s**\nCron: `%s` | 下次执行: %s | 状态: %s",
				t.Name, t.Schedule, nextRun, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("\n> %s", desc)
			}
			elements = append(elements, map[string]interface{}{"tag": "markdown", "content": row})

			if t.Status == "paused" {
				elements = append(elements, taskButtonRow(
					taskButton("▶ 恢复", "primary", "resume", t.ID),
					taskButton("🗑 删除", "danger", "delete", t.ID),
				))
			} else {
				elements = append(elements, taskButtonRow(
					taskButton("⏸ 暂停", "danger", "pause", t.ID),
					taskButton("🗑 删除", "danger", "delete", t.ID),
				))
			}
		}
	}

	if len(oneTimeTasks) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag": "markdown", "content": "📌 **一次性任务**",
		})
		for _, t := range oneTimeTasks {
			runAt := t.RunAt
			if runAt == "" {
				runAt = "-"
			}
			row := fmt.Sprintf("**%s**\n计划时间: %s | 状态: %s", t.Name, runAt, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("\n> %s", desc)
			}
			elements = append(elements, map[string]interface{}{"tag": "markdown", "content": row})
			elements = append(elements, taskButtonRow(
				taskButton("🗑 删除", "danger", "delete", t.ID),
			))
		}
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title":    map[string]interface{}{"tag": "plain_text", "content": fmt.Sprintf("📋 任务列表 (%d)", len(summaries))},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

// --- New task form card ---

// Form select options for /cron-new
var (
	freqOptions = []map[string]interface{}{
		{"text": map[string]string{"tag": "plain_text", "content": "每日"}, "value": "daily"},
		{"text": map[string]string{"tag": "plain_text", "content": "每工作日"}, "value": "weekdays"},
		{"text": map[string]string{"tag": "plain_text", "content": "每周"}, "value": "weekly"},
		{"text": map[string]string{"tag": "plain_text", "content": "每月"}, "value": "monthly"},
	}
	weekdayOptions = []map[string]interface{}{
		{"text": map[string]string{"tag": "plain_text", "content": "不选"}, "value": ""},
		{"text": map[string]string{"tag": "plain_text", "content": "周一"}, "value": "w1"},
		{"text": map[string]string{"tag": "plain_text", "content": "周二"}, "value": "w2"},
		{"text": map[string]string{"tag": "plain_text", "content": "周三"}, "value": "w3"},
		{"text": map[string]string{"tag": "plain_text", "content": "周四"}, "value": "w4"},
		{"text": map[string]string{"tag": "plain_text", "content": "周五"}, "value": "w5"},
		{"text": map[string]string{"tag": "plain_text", "content": "周六"}, "value": "w6"},
		{"text": map[string]string{"tag": "plain_text", "content": "周日"}, "value": "w0"},
	}
	monthDayOptions = func() []map[string]interface{} {
		opts := make([]map[string]interface{}, 28)
		for i := 1; i <= 28; i++ {
			opts[i-1] = map[string]interface{}{
				"text":  map[string]string{"tag": "plain_text", "content": fmt.Sprintf("%d号", i)},
				"value": fmt.Sprintf("d%d", i),
			}
		}
		return opts
	}()
	hourOptions = func() []map[string]interface{} {
		opts := make([]map[string]interface{}, 24)
		for i := 0; i < 24; i++ {
			opts[i] = map[string]interface{}{
				"text":  map[string]string{"tag": "plain_text", "content": fmt.Sprintf("%02d:00", i)},
				"value": fmt.Sprintf("%d", i),
			}
		}
		return opts
	}()
)

// BuildNewTaskCardJSON builds a v2 form card for creating a new scheduled task.
func BuildNewTaskCardJSON() string {
	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title":    map[string]interface{}{"tag": "plain_text", "content": "📅 创建定时任务"},
		},
		"body": map[string]interface{}{
			"elements": []map[string]interface{}{
				{
					"tag":  "form",
					"name": "create_task_form",
					"elements": []map[string]interface{}{
						// Frequency type
						{"tag": "markdown", "content": "**执行频率**"},
						{
							"tag":            "select_static",
							"name":           "freq_type",
							"placeholder":    map[string]string{"tag": "plain_text", "content": "选择频率"},
							"options":        freqOptions,
							"initial_option": "daily",
						},
						// Day selector (weekday or month day)
						{"tag": "markdown", "content": "**执行日期**（每周选星期几，每月选几号）"},
						{
							"tag":             "multi_select_static",
							"name":            "day_value",
							"placeholder":     map[string]string{"tag": "plain_text", "content": "选择日期"},
							"options":         append(weekdayOptions, monthDayOptions...),
							"selected_values": []string{""},
						},
						// Hour selector
						{"tag": "markdown", "content": "**执行时间**"},
						{
							"tag":             "multi_select_static",
							"name":            "hour_value",
							"placeholder":     map[string]string{"tag": "plain_text", "content": "选择时间"},
							"options":         hourOptions,
							"selected_values": []string{"9"},
						},
						// Task prompt
						{"tag": "markdown", "content": "**任务描述**"},
						{
							"tag":         "input",
							"name":        "prompt",
							"placeholder": map[string]string{"tag": "plain_text", "content": "描述需要 Claude 执行的任务内容"},
							"max_length":  500,
							"rows":        3,
						},
						// Submit button
						{
							"tag":              "button",
							"text":             map[string]string{"tag": "plain_text", "content": "✅ 创建任务"},
							"type":             "primary",
							"form_action_type": "submit",
							"name":             "submit_btn",
							"value":            map[string]string{"action": "create_task"},
							"behaviors":        []map[string]interface{}{{"type": "callback", "value": map[string]string{"action": "create_task"}}},
						},
					},
				},
			},
		},
	}

	bs, err := json.Marshal(card)
	if err != nil {
		slog.Error("feishu: failed to marshal new task card", "error", err)
		return "{}"
	}
	return string(bs)
}

// buildCreateTaskPrompt constructs a prompt for Claude to create a task via CLI.
func buildCreateTaskPrompt(taskName, cronExpr, prompt string) string {
	if taskName == "" {
		taskName = "定时任务"
	}
	return fmt.Sprintf(`请使用 bubbles CLI 创建一个定时任务，执行以下命令：

bubbles create -n %q -s %q -p %q

请直接执行命令并报告结果。如果创建成功，展示任务信息；如果失败，展示错误原因。`,
		taskName, cronExpr, prompt)
}

// --- Streaming card state ---

// cardState accumulates thinking and final text for a card stream.
type cardState struct {
	mu          sync.Mutex
	thinking    strings.Builder
	finalText   strings.Builder
	gotResult   bool
	extraBodies []string // extra card bodies when content is split
}

const (
	cardThinkingMaxLen = 8000
	// feishuMaxTables is the maximum number of table components allowed per Feishu card.
	// Exceeding this limit triggers API error 11310 ("card table number over limit").
	feishuMaxTables = 5
	// feishuMaxCardBytes is the maximum size of a Feishu card message (30 KB).
	feishuMaxCardBytes = 30000
)

func newCardState() *cardState {
	return &cardState{}
}

func (s *cardState) SetThinking(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thinking.Reset()
	s.thinking.WriteString(text)
}

func (s *cardState) SetFinal(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotResult = true
	s.finalText.WriteString(text)
}

// splitCardBodies splits markdown content into multiple chunks that each fit
// within a Feishu card. It splits on table boundaries (max feishuMaxTables per
// chunk) and newline boundaries (max maxBytes per chunk).
func splitCardBodies(content string, maxBytes int) []string {
	lines := strings.Split(content, "\n")
	type tableBlock struct{ start, end int }

	// Find all table blocks
	var tables []tableBlock
	inTable := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isRow := strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
		if isRow && !inTable {
			tables = append(tables, tableBlock{start: i})
			inTable = true
		} else if !isRow && inTable {
			tables[len(tables)-1].end = i
			inTable = false
		}
	}
	if inTable {
		tables[len(tables)-1].end = len(lines)
	}

	// Split into chunks at table boundaries when table count exceeds limit
	var chunks []string
	if len(tables) <= feishuMaxTables {
		chunks = append(chunks, content)
	} else {
		slog.Info("feishu: table count exceeds limit, splitting into multiple cards",
			"total", len(tables), "limit", feishuMaxTables)
		chunkStart := 0
		tableCount := 0
		for _, t := range tables {
			tableCount++
			if tableCount > feishuMaxTables {
				chunks = append(chunks, strings.Join(lines[chunkStart:t.start], "\n"))
				chunkStart = t.start
				tableCount = 1
			}
		}
		chunks = append(chunks, strings.Join(lines[chunkStart:], "\n"))
	}

	// Further split any chunk that exceeds maxBytes at newline boundaries
	var result []string
	for _, chunk := range chunks {
		for len(chunk) > maxBytes {
			cut := chunk[:maxBytes]
			if idx := strings.LastIndex(cut, "\n"); idx > maxBytes/2 {
				cut = cut[:idx]
			}
			result = append(result, cut)
			chunk = strings.TrimLeft(chunk[len(cut):], "\n")
		}
		if chunk != "" {
			result = append(result, chunk)
		}
	}
	return result
}

func (s *cardState) BuildCard() string {
	return s.buildCardWithPrefix("")
}

// BuildExtraCards returns card JSONs for extra content bodies that didn't fit
// in the main card. Returns nil if no extras. Each call drains the extras.
func (s *cardState) BuildExtraCards() []string {
	return s.buildExtraCardsWithPrefix("")
}

// BuildCardWithName is like BuildCard but includes the session name in the header.
func (s *cardState) BuildCardWithName(sessionName string) string {
	if sessionName == "" {
		return s.BuildCard()
	}
	return s.buildCardWithPrefix(sessionName)
}

// buildCardWithPrefix is the shared implementation for BuildCard and BuildCardWithName.
func (s *cardState) buildCardWithPrefix(sessionName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	thinking := s.thinking.String()
	if len(thinking) > cardThinkingMaxLen {
		thinking = thinking[:cardThinkingMaxLen] + "\n\n... (省略)"
	}
	final := s.finalText.String()
	hasFinal := s.finalText.Len() > 0

	headerTitle := "🤖 Claude Code"
	if sessionName != "" {
		headerTitle = "🤖 Claude Code — " + sessionName
	}
	if hasFinal {
		if sessionName != "" {
			headerTitle = "✅ Claude Code — " + sessionName
		} else {
			headerTitle = "✅ Claude Code"
		}
	}

	if !hasFinal && thinking == "" {
		thinking = "开始执行任务，思考中..."
	}

	var elements []map[string]interface{}
	if thinking != "" {
		elements = append(elements, map[string]interface{}{
			"tag":     "markdown",
			"content": "💭 **思考过程**\n\n" + thinking,
		})
	}

	if hasFinal && final != "" {
		maxFinal := feishuMaxCardBytes - len(thinking) - 500
		if maxFinal < 2000 {
			maxFinal = 2000
		}
		bodies := splitCardBodies(final, maxFinal)
		final = bodies[0]
		if len(bodies) > 1 {
			s.extraBodies = bodies[1:]
		}
		elements = append(elements,
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag":     "markdown",
				"content": "📝 **最终回复**\n\n" + final,
			},
		)
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": headerTitle,
			},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, err := json.Marshal(card)
	if err != nil {
		slog.Error("feishu: failed to marshal card", "error", err)
		return "{}"
	}
	return string(bs)
}

// BuildExtraCardsWithName is like BuildExtraCards but includes the session name in the header.
func (s *cardState) BuildExtraCardsWithName(sessionName string) []string {
	if sessionName == "" {
		return s.BuildExtraCards()
	}
	return s.buildExtraCardsWithPrefix(sessionName)
}

// buildExtraCardsWithPrefix is the shared implementation for BuildExtraCards and BuildExtraCardsWithName.
func (s *cardState) buildExtraCardsWithPrefix(sessionName string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.extraBodies) == 0 {
		return nil
	}
	extras := s.extraBodies
	s.extraBodies = nil

	title := "✅ Claude Code (续)"
	if sessionName != "" {
		title = fmt.Sprintf("✅ Claude Code — %s (续)", sessionName)
	}
	cards := make([]string, 0, len(extras))
	for _, body := range extras {
		card := map[string]interface{}{
			"schema": "2.0",
			"header": map[string]interface{}{
				"template": "blue",
				"title":    map[string]interface{}{"tag": "plain_text", "content": title},
			},
			"body": map[string]interface{}{
				"elements": []map[string]interface{}{
					{"tag": "markdown", "content": body},
				},
			},
		}
		bs, err := json.Marshal(card)
		if err != nil {
			slog.Error("feishu: failed to marshal extra card", "error", err)
			continue
		}
		cards = append(cards, string(bs))
	}
	return cards
}

// sessionButton creates a button for session actions (switch/close).
func sessionButton(text, buttonType, action, sessionKey string) map[string]interface{} {
	value := map[string]string{"action": action, "session_key": sessionKey}
	return map[string]interface{}{
		"tag":       "button",
		"text":      map[string]string{"tag": "plain_text", "content": text},
		"type":      buttonType,
		"value":     value,
		"behaviors": []map[string]interface{}{{"type": "callback", "value": value}},
	}
}

// BuildSessionsCard builds a Feishu v2 card showing all sessions for a chat.
func BuildSessionsCard(sessions []*ChatSession, activeKey string) string {
	if len(sessions) == 0 {
		card := map[string]interface{}{
			"schema": "2.0",
			"header": map[string]interface{}{
				"template": "blue",
				"title":    map[string]interface{}{"tag": "plain_text", "content": "📋 会话列表"},
			},
			"body": map[string]interface{}{
				"elements": []map[string]interface{}{
					{"tag": "markdown", "content": "暂无会话。发送消息将自动创建一个新会话。"},
				},
			},
		}
		bs, _ := json.Marshal(card)
		return string(bs)
	}

	var elements []map[string]interface{}
	for _, s := range sessions {
		activeMark := ""
		if s.key == activeKey {
			activeMark = " ✅ **(当前)**"
		}

		age := time.Since(s.created).Truncate(time.Minute)
		idle := time.Since(s.lastActive).Truncate(time.Minute)
		info := fmt.Sprintf("**%s**%s\n创建: %s 前 | 最后活跃: %s 前",
			s.name, activeMark, formatDuration(age), formatDuration(idle))
		if s.firstMessage != "" {
			info += fmt.Sprintf("\n> %s", s.firstMessage)
		}

		elements = append(elements, map[string]interface{}{
			"tag":     "markdown",
			"content": info,
		})

		// Buttons: switch (if not active) and close
		var buttons []map[string]interface{}
		if s.key != activeKey {
			buttons = append(buttons, sessionButton("切换", "primary", "switch_session", s.key))
		}
		buttons = append(buttons, sessionButton("关闭", "danger", "close_session", s.key))
		elements = append(elements, taskButtonRow(buttons...))
		elements = append(elements, map[string]interface{}{"tag": "hr"})
	}

	// Remove trailing hr
	if len(elements) > 0 && elements[len(elements)-1]["tag"] == "hr" {
		elements = elements[:len(elements)-1]
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title":    map[string]interface{}{"tag": "plain_text", "content": "📋 会话列表"},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, err := json.Marshal(card)
	if err != nil {
		slog.Error("feishu: failed to marshal sessions card", "error", err)
		return "{}"
	}
	return string(bs)
}

// formatDuration formats a duration in a human-readable Chinese format.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

// splitMessage splits a long string into chunks of at most maxLen runes,
// trying to break at newlines.
func splitMessage(s string, maxLen int) []string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return []string{s}
	}

	var chunks []string
	for len(runes) > maxLen {
		// Try to find a newline near the limit
		breakIdx := -1
		for i := maxLen - 1; i >= maxLen/2; i-- {
			if runes[i] == '\n' {
				breakIdx = i
				break
			}
		}
		if breakIdx == -1 {
			breakIdx = maxLen
		}
		chunks = append(chunks, string(runes[:breakIdx]))
		runes = runes[breakIdx:]
		// Skip leading newline on next chunk
		if len(runes) > 0 && runes[0] == '\n' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}

// --- Task completion notification card ---

// BuildTaskCompletionCard builds a v2 card for task completion notification.
func BuildTaskCompletionCard(c daemon.TaskCompletion) []string {
	taskName := c.TaskName
	if taskName == "" {
		taskName = c.TaskID
	}

	statusEmoji := "✅"
	statusText := "成功"
	headerColor := "green"
	if c.Status == "failed" {
		statusEmoji = "❌"
		statusText = "失败"
		headerColor = "red"
	}

	duration := c.Duration.Round(time.Second)
	startTime := c.StartedAt.Format("15:04:05")
	endTime := c.EndedAt.Format("15:04:05")

	// Truncate output if too long (using runes for proper UTF-8 handling)
	output := c.Output
	maxOutput := 3000
	outputRunes := []rune(output)
	if len(outputRunes) > maxOutput {
		output = string(outputRunes[:maxOutput]) + "\n\n... (输出被截断)"
	}

	info := fmt.Sprintf("%s **任务完成: %s**\n"+
		"📋 Task ID: `%s`\n"+
		"📊 状态: %s %s\n"+
		"⏱️ 耗时: %s\n"+
		"🕐 开始: %s | 结束: %s",
		statusEmoji, taskName,
		c.TaskID,
		statusEmoji, statusText,
		duration,
		startTime, endTime,
	)

	// Split output into multiple card bodies to respect Feishu limits
	bodies := splitCardBodies(output, feishuMaxCardBytes-500)
	cardTitle := fmt.Sprintf("%s 任务完成: %s", statusEmoji, taskName)

	// First card: info + first body
	elements := []map[string]interface{}{
		{"tag": "markdown", "content": info},
		{"tag": "hr"},
		{"tag": "markdown", "content": "📝 **执行输出**\n\n" + bodies[0]},
	}
	firstCard := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": headerColor,
			"title":    map[string]interface{}{"tag": "plain_text", "content": cardTitle},
		},
		"body": map[string]interface{}{"elements": elements},
	}
	firstJSON, err := json.Marshal(firstCard)
	if err != nil {
		slog.Error("feishu: failed to marshal task completion card", "error", err)
		return []string{"{}"}
	}
	cards := []string{string(firstJSON)}

	// Extra cards for remaining bodies
	for _, body := range bodies[1:] {
		extraCard := map[string]interface{}{
			"schema": "2.0",
			"header": map[string]interface{}{
				"template": headerColor,
				"title":    map[string]interface{}{"tag": "plain_text", "content": cardTitle + " (续)"},
			},
			"body": map[string]interface{}{
				"elements": []map[string]interface{}{
					{"tag": "markdown", "content": body},
				},
			},
		}
		bs, err := json.Marshal(extraCard)
		if err != nil {
			slog.Error("feishu: failed to marshal extra task completion card", "error", err)
			continue
		}
		cards = append(cards, string(bs))
	}
	return cards
}
