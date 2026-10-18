package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
	"github.com/pauluszhou/bubbles/internal/model"
)

var holdingCmd = &cobra.Command{
	Use:     "holding",
	Short:   "查看与修改持仓（SQLite 持仓库）",
	Aliases: []string{"holdings", "pos"},
}

var holdingListCmd = &cobra.Command{
	Use:     "list",
	Short:   "列出全部持仓",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := ipcCall(ipc.MethodHoldingList, nil)
		if err != nil {
			return err
		}
		var result struct {
			Holdings []model.Holding `json:"holdings"`
			Count    int             `json:"count"`
		}
		if err := unmarshalResult(resp, &result); err != nil {
			return err
		}
		if result.Count == 0 {
			fmt.Println("(no holdings)")
			return nil
		}
		fmt.Printf("%-14s %-12s %10s %10s %10s %10s %10s %-12s %s\n",
			"TICKER", "NAME", "QTY", "COST", "STATE", "STOP", "TARGET", "REVIEW", "UPDATED")
		for _, h := range result.Holdings {
			fmt.Printf("%-14s %-12s %10s %10s %10s %10s %10s %-12s %s\n",
				h.Ticker, trunc(h.Name, 12), fmtFloat(h.Qty), fmtFloat(h.Cost),
				h.State, fmtFloat(h.StopLoss), fmtFloat(h.Target), h.ReviewDate, h.UpdatedAt)
		}
		return nil
	},
}

var holdingGetCmd = &cobra.Command{
	Use:     "get <ticker>",
	Short:   "查看单个持仓详情",
	Aliases: []string{"show"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := ipcCall(ipc.MethodHoldingGet, ipc.HoldingGetParams{Ticker: args[0]})
		if err != nil {
			return err
		}
		h, err := resultHolding(resp)
		if err != nil {
			return err
		}
		printHolding(h)
		return nil
	},
}

var holdingSetCmd = &cobra.Command{
	Use:   "set <ticker>",
	Short: "新增或修改持仓字段（未指定的字段保持不变；新建需 --name）",
	Args:  cobra.ExactArgs(1),
	Example: `  bubbles holding set HK.01810 --name 小米集团 --market 港股 --qty 15800 --cost 31.335
  bubbles holding set HK.01810 --cost 31.335 --stop-loss 25.08 --state holding
  bubbles holding set 601689.SH --name 拓普集团 --state watchlist --target 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fs := cmd.Flags()
		params := ipc.HoldingUpsertParams{Ticker: args[0]}

		strField := func(flag string, dst **string) {
			if fs.Changed(flag) {
				v, _ := fs.GetString(flag)
				*dst = &v
			}
		}
		numField := func(flag string, dst **float64) {
			if fs.Changed(flag) {
				v, _ := fs.GetFloat64(flag)
				*dst = &v
			}
		}
		strField("name", &params.Name)
		strField("market", &params.Market)
		strField("state", &params.State)
		strField("hypothesis", &params.Hypothesis)
		strField("review-date", &params.ReviewDate)
		strField("decision", &params.Decision)
		strField("notes", &params.Notes)
		numField("qty", &params.Qty)
		numField("cost", &params.Cost)
		numField("stop-loss", &params.StopLoss)
		numField("target", &params.Target)

		resp, err := ipcCall(ipc.MethodHoldingUpsert, params)
		if err != nil {
			return err
		}
		var result ipc.HoldingResult
		if err := unmarshalResult(resp, &result); err != nil {
			return err
		}
		h, err := resultHolding(resp)
		if err != nil {
			return err
		}
		verb := "updated"
		if result.Created {
			verb = "created"
		}
		fmt.Printf("✓ holding %s %s\n", h.Ticker, verb)
		printHolding(h)
		return nil
	},
}

var holdingRmCmd = &cobra.Command{
	Use:     "rm <ticker>",
	Short:   "删除持仓",
	Aliases: []string{"del", "delete"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := ipcCall(ipc.MethodHoldingDelete, ipc.HoldingGetParams{Ticker: args[0]}); err != nil {
			return err
		}
		fmt.Printf("✓ holding %s deleted\n", args[0])
		return nil
	},
}

func init() {
	holdingSetCmd.Flags().String("name", "", "标的名称")
	holdingSetCmd.Flags().String("market", "", "市场：港股/A股/美股")
	holdingSetCmd.Flags().String("state", "", "状态：holding/watchlist/validating/pending_decision/falsified/sold")
	holdingSetCmd.Flags().String("hypothesis", "", "假设（可证伪陈述）")
	holdingSetCmd.Flags().String("review-date", "", "复核日期 YYYY-MM-DD")
	holdingSetCmd.Flags().String("decision", "", "决策日志 ID（如 D-20261018-01）")
	holdingSetCmd.Flags().String("notes", "", "备注")
	holdingSetCmd.Flags().Float64("qty", 0, "持仓数量")
	holdingSetCmd.Flags().Float64("cost", 0, "成本价")
	holdingSetCmd.Flags().Float64("stop-loss", 0, "止损价")
	holdingSetCmd.Flags().Float64("target", 0, "目标价")

	holdingCmd.AddCommand(holdingListCmd, holdingGetCmd, holdingSetCmd, holdingRmCmd)
}

// ipcCall is a thin wrapper shared by holding subcommands.
func ipcCall(method string, params interface{}) (*ipc.Response, error) {
	client := ipc.NewClient(daemon.SocketPath())
	resp, err := client.Call(method, params)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func unmarshalResult(resp *ipc.Response, out interface{}) error {
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func resultHolding(resp *ipc.Response) (*model.Holding, error) {
	var result ipc.HoldingResult
	if err := unmarshalResult(resp, &result); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result.Holding)
	if err != nil {
		return nil, err
	}
	var h model.Holding
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func printHolding(h *model.Holding) {
	fmt.Printf("  ticker:     %s\n", h.Ticker)
	fmt.Printf("  name:       %s\n", h.Name)
	fmt.Printf("  market:     %s\n", h.Market)
	fmt.Printf("  qty:        %s\n", fmtFloat(h.Qty))
	fmt.Printf("  cost:       %s\n", fmtFloat(h.Cost))
	fmt.Printf("  state:      %s\n", h.State)
	fmt.Printf("  hypothesis: %s\n", h.Hypothesis)
	fmt.Printf("  stop_loss:  %s\n", fmtFloat(h.StopLoss))
	fmt.Printf("  target:     %s\n", fmtFloat(h.Target))
	fmt.Printf("  review:     %s\n", h.ReviewDate)
	fmt.Printf("  decision:   %s\n", h.Decision)
	fmt.Printf("  notes:      %s\n", h.Notes)
	fmt.Printf("  updated:    %s\n", h.UpdatedAt)
}

func fmtFloat(v *float64) string {
	if v == nil {
		return "-"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", *v), "0"), ".")
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
