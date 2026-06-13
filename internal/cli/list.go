package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有任务",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskList, nil)
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}

		data, _ := json.Marshal(resp.Result)
		var tasks []ipc.TaskResult
		json.Unmarshal(data, &tasks)

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return nil
		}

		fmt.Printf("%-20s %-20s %-10s %-20s %-20s\n", "ID", "NAME", "STATUS", "SCHEDULE", "NEXT RUN")
		fmt.Println(string(make([]byte, 90))) // 分隔线
		for _, t := range tasks {
			schedule := t.Schedule
			if schedule == "" {
				schedule = "(one-time)"
			}
			nextRun := t.NextRunAt
			if nextRun == "" || nextRun == "0001-01-01T00:00:00Z" {
				nextRun = "-"
			}
			fmt.Printf("%-20s %-20s %-10s %-20s %-20s\n", t.ID, t.Name, t.Status, schedule, nextRun)
		}
		return nil
	},
}
