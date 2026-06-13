package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var (
	logsLast bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "查看任务执行日志",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskLogs, ipc.ListLogsParams{
			TaskID: args[0],
			Last:   logsLast,
		})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}

		data, _ := json.Marshal(resp.Result)
		var logs []ipc.ExecutionLogResult
		json.Unmarshal(data, &logs)

		if len(logs) == 0 {
			fmt.Println("No execution logs found")
			return nil
		}

		for _, l := range logs {
			fmt.Printf("━━━ Execution %s ━━━\n", l.ID)
			fmt.Printf("Status:    %s\n", l.Status)
			fmt.Printf("Started:   %s\n", l.StartedAt)
			if l.EndedAt != "" {
				fmt.Printf("Ended:     %s\n", l.EndedAt)
			}
			fmt.Printf("\n%s\n\n", l.Output)
		}
		return nil
	},
}

func init() {
	logsCmd.Flags().BoolVar(&logsLast, "last", false, "只显示最近一次执行日志")
}
