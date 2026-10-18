package feishu

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/model"
)

// HoldingStore is the subset of store.Store used by the /holding command.
type HoldingStore interface {
	ListHoldings() ([]model.Holding, error)
	GetHolding(ticker string) (*model.Holding, error)
	UpsertHolding(ticker string, fields map[string]any) (bool, error)
	DeleteHolding(ticker string) error
}

// holdingFieldAliases maps user-facing keys (both Chinese and English) to
// whitelisted holding columns and their value kinds.
var holdingFieldAliases = map[string]struct {
	col string
	num bool
}{
	"名称": {"name", false}, "name": {"name", false},
	"市场": {"market", false}, "market": {"market", false},
	"数量": {"qty", true}, "qty": {"qty", true}, "股数": {"qty", true},
	"成本": {"cost", true}, "cost": {"cost", true},
	"状态": {"state", false}, "state": {"state", false},
	"假设": {"hypothesis", false}, "hypothesis": {"hypothesis", false},
	"止损": {"stop_loss", true}, "stop": {"stop_loss", true}, "stop_loss": {"stop_loss", true},
	"目标": {"target", true}, "target": {"target", true},
	"复核": {"review_date", false}, "review": {"review_date", false},
	"决策": {"decision", false}, "decision": {"decision", false},
	"备注": {"notes", false}, "note": {"notes", false}, "notes": {"notes", false},
}

// HandleHoldingCommand implements "/holding" — view and edit holdings stored in
// the bubblesd SQLite database.
//
//	/holding                     列表
//	/holding 显示 HK.01810       详情
//	/holding 新增 HK.01810 名称=小米集团 市场=港股 数量=15800 成本=31.335
//	/holding 修改 HK.01810 成本=31.335 止损=25.08 状态=holding
//	/holding 删除 HK.01810 确认
func HandleHoldingCommand(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage, st HoldingStore) error {
	trimmed := StripMentionPrefix(msg.Content)
	args := strings.Fields(trimmed)

	sub := "列表"
	if len(args) > 1 {
		sub = args[1]
	}

	var reply string
	switch sub {
	case "列表", "list", "ls":
		reply, _ = holdingListMarkdown(st)
	case "显示", "详情", "get", "show":
		if len(args) < 3 {
			reply = "⚠️ 用法：/holding 显示 <ticker>"
		} else {
			reply = holdingDetailMarkdown(st, args[2])
		}
	case "新增", "添加", "add":
		reply = holdingUpsert(st, args[2:], true)
	case "修改", "更新", "set", "update":
		reply = holdingUpsert(st, args[2:], false)
	case "删除", "rm", "del", "delete":
		reply = holdingDelete(st, args[2:])
	case "帮助", "help":
		reply = holdingHelp()
	default:
		// 允许省略动词：/holding HK.01810 直接显示详情
		if strings.Contains(sub, ".") || strings.Contains(sub, ":") || isAllDigits(sub) {
			reply = holdingDetailMarkdown(st, sub)
		} else {
			reply = holdingHelp()
		}
	}

	_, err := ch.Send(ctx, &types.SendInput{
		ChatID:         msg.ChatID,
		Markdown:       reply,
		ReplyMessageID: msg.MessageID,
	})
	return err
}

func holdingHelp() string {
	return "📖 **持仓命令**\n\n" +
		"• `/holding` — 持仓列表\n" +
		"• `/holding 显示 HK.01810` — 单个持仓详情\n" +
		"• `/holding 新增 HK.01810 名称=小米集团 市场=港股 数量=15800 成本=31.335` — 新增（名称必填）\n" +
		"• `/holding 修改 HK.01810 成本=31.335 止损=25.08 状态=holding 复核=2026-11-30` — 局部修改\n" +
		"• `/holding 删除 HK.01810 确认` — 删除（需二次确认）\n\n" +
		"可用字段：名称/市场/数量/成本/状态/假设/止损/目标/复核/决策/备注（空格分隔 key=value，值不含空格）\n" +
		"状态取值：holding / watchlist / validating / pending_decision / falsified / sold"
}

func holdingListMarkdown(st HoldingStore) (string, error) {
	holdings, err := st.ListHoldings()
	if err != nil {
		return fmt.Sprintf("❌ 查询持仓失败: %v", err), nil
	}
	if len(holdings) == 0 {
		return "📭 暂无持仓，用 `/holding 新增 <ticker> 名称=xx` 添加", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📋 **持仓列表**（%d 条）\n\n", len(holdings)))
	b.WriteString("| 标的 | 名称 | 数量 | 成本 | 状态 | 止损 | 目标 | 复核 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, h := range holdings {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			h.Ticker, h.Name, f2s(h.Qty), f2s(h.Cost), h.State,
			f2s(h.StopLoss), f2s(h.Target), dash(h.ReviewDate)))
	}
	b.WriteString("\n💡 `显示 <ticker>` 看详情，`修改 <ticker> 字段=值` 修改")
	return b.String(), nil
}

func holdingDetailMarkdown(st HoldingStore, ticker string) string {
	h, err := st.GetHolding(ticker)
	if err != nil {
		return fmt.Sprintf("❌ 未找到持仓 **%s**（`/holding 新增 %s 名称=xx` 可创建）", ticker, ticker)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📌 **%s（%s）**\n\n", h.Name, h.Ticker))
	b.WriteString("| 字段 | 值 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| 市场 | %s |\n", dash(h.Market)))
	b.WriteString(fmt.Sprintf("| 数量 | %s |\n", f2s(h.Qty)))
	b.WriteString(fmt.Sprintf("| 成本 | %s |\n", f2s(h.Cost)))
	b.WriteString(fmt.Sprintf("| 状态 | %s |\n", dash(h.State)))
	b.WriteString(fmt.Sprintf("| 止损 | %s |\n", f2s(h.StopLoss)))
	b.WriteString(fmt.Sprintf("| 目标 | %s |\n", f2s(h.Target)))
	b.WriteString(fmt.Sprintf("| 复核日 | %s |\n", dash(h.ReviewDate)))
	b.WriteString(fmt.Sprintf("| 决策ID | %s |\n", dash(h.Decision)))
	if h.Hypothesis != "" {
		b.WriteString(fmt.Sprintf("| 假设 | %s |\n", h.Hypothesis))
	}
	if h.Notes != "" {
		b.WriteString(fmt.Sprintf("| 备注 | %s |\n", h.Notes))
	}
	b.WriteString(fmt.Sprintf("| 更新于 | %s |", h.UpdatedAt))
	return b.String()
}

func holdingUpsert(st HoldingStore, args []string, creating bool) string {
	if len(args) < 1 {
		return "⚠️ 缺少 ticker，用法见 `/holding 帮助`"
	}
	ticker := args[0]
	fields := map[string]any{}
	for _, kv := range args[1:] {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Sprintf("⚠️ 无法解析字段 `%s`（需 key=value 格式）", kv)
		}
		meta, known := holdingFieldAliases[key]
		if !known {
			return fmt.Sprintf("⚠️ 未知字段 `%s`，可用：名称/市场/数量/成本/状态/假设/止损/目标/复核/决策/备注", key)
		}
		if meta.num {
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Sprintf("⚠️ 字段 `%s` 需要数字，收到 `%s`", key, val)
			}
			fields[meta.col] = f
		} else {
			fields[meta.col] = val
		}
	}
	if creating {
		if _, ok := fields["name"]; !ok {
			return "⚠️ 新增持仓必须提供 名称=xxx"
		}
	} else {
		if _, err := st.GetHolding(ticker); err != nil {
			return fmt.Sprintf("❌ 未找到持仓 **%s**（新增请用 `/holding 新增 %s 名称=xx`）", ticker, ticker)
		}
		if len(fields) == 0 {
			return "⚠️ 没有要修改的字段，用法见 `/holding 帮助`"
		}
	}
	created, err := st.UpsertHolding(ticker, fields)
	if err != nil {
		return fmt.Sprintf("❌ 保存失败: %v", err)
	}
	verb := "已更新"
	if created {
		verb = "已新增"
	}
	return fmt.Sprintf("✅ **%s** %s\n\n%s", ticker, verb, holdingDetailMarkdown(st, ticker))
}

func holdingDelete(st HoldingStore, args []string) string {
	if len(args) < 1 {
		return "⚠️ 用法：/holding 删除 <ticker> 确认"
	}
	ticker := args[0]
	if len(args) < 2 || args[1] != "确认" {
		return fmt.Sprintf("⚠️ 删除是破坏性操作，请输入：`/holding 删除 %s 确认`", ticker)
	}
	if err := st.DeleteHolding(ticker); err != nil {
		return fmt.Sprintf("❌ 删除失败: %v", err)
	}
	return fmt.Sprintf("🗑️ 持仓 **%s** 已删除", ticker)
}

func f2s(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
