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
							"tag":           "select_static",
							"name":          "freq_type",
							"placeholder":   map[string]string{"tag": "plain_text", "content": "选择频率"},
							"options":       freqOptions,
							"initial_option": "daily",
						},
						// Day selector (weekday or month day)
						{"tag": "markdown", "content": "**执行日期**（每周选星期几，每月选几号）"},
						{
							"tag":            "multi_select_static",
							"name":           "day_value",
							"placeholder":    map[string]string{"tag": "plain_text", "content": "选择日期"},
							"options":        append(weekdayOptions, monthDayOptions...),
							"selected_values": []string{""},
						},
						// Hour selector
						{"tag": "markdown", "content": "**执行时间**"},
						{
							"tag":            "multi_select_static",
							"name":           "hour_value",
							"placeholder":    map[string]string{"tag": "plain_text", "content": "选择时间"},
							"options":        hourOptions,
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
							"tag":   "button",
							"text":  map[string]string{"tag": "plain_text", "content": "✅ 创建任务"},
							"type":  "primary",
							"form_action_type": "submit",
							"name":       "submit_btn",
							"value":      map[string]string{"action": "create_task"},
							"behaviors":  []map[string]interface{}{{"type": "callback", "value": map[string]string{"action": "create_task"}}},
						},
					},
				},
			},
		},
	}

	bs, _ := json.Marshal(card)
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
	mu        sync.Mutex
	thinking  strings.Builder
	finalText strings.Builder
	gotResult bool
}

const cardThinkingMaxLen = 8000

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

// thinkingTruncated returns truncated thinking content with ellipsis if too long.
func (s *cardState) thinkingTruncated() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.thinking.String()
	if len(text) <= cardThinkingMaxLen {
		return text
	}
	return text[:cardThinkingMaxLen] + "\n\n... (省略)"
}

func (s *cardState) finalContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalText.String()
}

func (s *cardState) BuildCard() string {
	s.mu.Lock()
	hasFinal := s.finalText.Len() > 0
	s.mu.Unlock()

	thinking := s.thinkingTruncated()
	final := s.finalContent()

	headerTitle := "🤖 Claude Code"
	if hasFinal {
		headerTitle = "✅ Claude Code"
	}

	// Build v2 card JSON with markdown tag for proper rendering
	elements := []map[string]interface{}{
		{
			"tag":     "markdown",
			"content": "💭 **思考过程**\n\n" + thinking,
		},
	}

	if hasFinal {
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

	bs, _ := json.Marshal(card)
	result := string(bs)

	slog.Debug("feishu: card JSON", "card", result)
	if len(result) > 2000 {
		slog.Info("feishu: card JSON truncated", "preview", result[:2000])
	} else {
		slog.Info("feishu: card JSON", "card", result)
	}
	return result
}

// splitMessage splits a long string into chunks of at most maxLen characters,
// trying to break at newlines.
func splitMessage(s string, maxLen int) []string {
	if len(s) <= maxLen {
		return []string{s}
	}

	var chunks []string
	for len(s) > maxLen {
		// Try to find a newline near the limit
		breakIdx := strings.LastIndex(s[:maxLen], "\n")
		if breakIdx == -1 || breakIdx < maxLen/2 {
			breakIdx = maxLen
		}
		chunks = append(chunks, s[:breakIdx])
		s = s[breakIdx:]
		// Skip leading newline on next chunk
		s = strings.TrimPrefix(s, "\n")
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}

// --- Task completion notification card ---

// BuildTaskCompletionCard builds a v2 card for task completion notification.
func BuildTaskCompletionCard(c daemon.TaskCompletion) string {
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

	// Truncate output if too long
	output := c.Output
	maxOutput := 3000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n\n... (输出被截断)"
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

	elements := []map[string]interface{}{
		{"tag": "markdown", "content": info},
		{"tag": "hr"},
		{"tag": "markdown", "content": "📝 **执行输出**\n\n" + output},
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": headerColor,
			"title":    map[string]interface{}{"tag": "plain_text", "content": fmt.Sprintf("%s 任务完成: %s", statusEmoji, taskName)},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, _ := json.Marshal(card)
	return string(bs)
}
