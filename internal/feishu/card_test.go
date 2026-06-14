package feishu

import (
	"strings"
	"testing"
)

func TestSplitCardBodies_TablesExceeded(t *testing.T) {
	// 10 tables — should split into 2 cards (5 tables each)
	content := buildTestContent(10)
	bodies := splitCardBodies(content, feishuMaxCardBytes)

	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 bodies for 10 tables, got %d", len(bodies))
	}

	// Count tables in each body
	for i, body := range bodies {
		count := countTables(body)
		t.Logf("body[%d]: %d tables, %d bytes", i, count, len(body))
		if count > feishuMaxTables {
			t.Errorf("body[%d] has %d tables, max %d", i, count, feishuMaxTables)
		}
	}

	// First 5 tables should be in the first body
	if !strings.Contains(bodies[0], "| 列A | 列B | 列C |") {
		t.Error("first table should be in first body")
	}

	// Second body should have remaining 5 tables
	if !strings.Contains(bodies[1], "| 列A | 列B | 列C |") {
		t.Error("second body should contain tables")
	}
}

func TestSplitCardBodies_NoOp(t *testing.T) {
	// 2 tables, small content — single body, unchanged
	content := `text

| a | b |
|---|---|
| 1 | 2 |

| c | d |
|---|---|
| 3 | 4 |
`
	bodies := splitCardBodies(content, feishuMaxCardBytes)
	if len(bodies) != 1 {
		t.Errorf("expected 1 body for 2 tables, got %d", len(bodies))
	}
	if bodies[0] != content {
		t.Error("content should pass through unchanged")
	}
}

func TestSplitCardBodies_SizeExceeded(t *testing.T) {
	// No tables but content exceeds maxBytes
	long := strings.Repeat("line of content here\n", 5000) // ~100KB
	bodies := splitCardBodies(long, 25000)

	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 bodies for 100KB content, got %d", len(bodies))
	}

	for i, body := range bodies {
		if len(body) > 26000 { // allow slack for truncation notice
			t.Errorf("body[%d] too large: %d bytes", i, len(body))
		}
	}
}

func TestSplitCardBodies_TablesAndSizeExceeded(t *testing.T) {
	// 10 tables + large content — should split on both table count and size
	content := buildTestContent(10)
	// Pad with extra text to also exceed size
	content += "\n\n" + strings.Repeat("extra padding line\n", 2000)

	bodies := splitCardBodies(content, 25000)

	if len(bodies) < 2 {
		t.Fatalf("expected >= 2 bodies, got %d", len(bodies))
	}

	totalTables := 0
	for i, body := range bodies {
		count := countTables(body)
		totalTables += count
		t.Logf("body[%d]: %d tables, %d bytes", i, count, len(body))
		if count > feishuMaxTables {
			t.Errorf("body[%d] has %d tables, max %d", i, count, feishuMaxTables)
		}
	}
	if totalTables != 10 {
		t.Errorf("expected 10 total tables, got %d", totalTables)
	}
}

func TestBuildExtraCards(t *testing.T) {
	state := newCardState()
	state.SetThinking("thinking...")
	state.SetFinal(buildTestContent(10))

	// BuildCard should populate extraBodies
	card := state.BuildCard()
	if card == "{}" {
		t.Fatal("BuildCard returned empty card")
	}

	extras := state.BuildExtraCards()
	if len(extras) < 1 {
		t.Fatal("expected extra cards for 10 tables")
	}

	for i, extra := range extras {
		if len(extra) < 100 {
			t.Errorf("extra card[%d] seems too short: %d bytes", i, len(extra))
		}
	}

	// Second call should return nil (drained)
	extras2 := state.BuildExtraCards()
	if extras2 != nil {
		t.Error("second BuildExtraCards call should return nil")
	}
}

func buildTestContent(tableCount int) string {
	var b strings.Builder
	b.WriteString("# 分析报告\n\n")
	for i := 0; i < tableCount; i++ {
		b.WriteString("## 标题\n\n")
		b.WriteString("| 列A | 列B | 列C |\n")
		b.WriteString("|-----|-----|-----|\n")
		for j := 0; j < 4; j++ {
			b.WriteString("| 数据 | 数据 | 数据 |\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func countTables(content string) int {
	lines := strings.Split(content, "\n")
	count := 0
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isRow := strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
		if isRow && !inTable {
			count++
			inTable = true
		} else if !isRow && inTable {
			inTable = false
		}
	}
	return count
}
