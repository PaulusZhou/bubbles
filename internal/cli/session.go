package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var sessionExportPath string

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "管理 pi 会话（统计 / 导出）",
}

var sessionStatsCmd = &cobra.Command{
	Use:   "stats <session-id>",
	Short: "查看 pi 会话的 token / 成本统计",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodSessionStats, ipc.SessionParams{SessionID: args[0]})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}

		data, _ := json.Marshal(resp.Result)
		var result ipc.SessionStatsResult
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}

		// Pretty-print the stats record; fall back to raw JSON.
		var out map[string]interface{}
		if err := json.Unmarshal(result.Stats, &out); err == nil {
			pretty, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(pretty))
		} else {
			fmt.Println(string(result.Stats))
		}
		return nil
	},
}

var sessionExportCmd = &cobra.Command{
	Use:   "export <session-id>",
	Short: "将 pi 会话导出为 HTML",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodSessionExport, ipc.SessionParams{
			SessionID: args[0],
			Path:      sessionExportPath,
		})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}

		data, _ := json.Marshal(resp.Result)
		var result ipc.SessionExportResult
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		fmt.Printf("✅ 会话已导出: %s\n", result.Path)
		return nil
	},
}

func init() {
	sessionExportCmd.Flags().StringVarP(&sessionExportPath, "output", "o", "", "导出文件路径（默认由 pi 决定）")
	sessionCmd.AddCommand(sessionStatsCmd)
	sessionCmd.AddCommand(sessionExportCmd)
}
