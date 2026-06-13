package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var (
	createName     string
	createPrompt   string
	createSchedule string
	createRunAt    string
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "创建新任务",
	Example: `  # 创建 cron 定时任务（每天 9 点执行）
  bubbles create --name "每日总结" --schedule "0 9 * * *" --prompt "总结今天的代码变更"

  # 创建一次性任务（指定时间执行，格式：YYYY-MM-DDTHH:MM:SS）
  bubbles create --name "代码审查" --at "2026-06-13T14:59:00" --prompt "运行代码审查"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if createPrompt == "" {
			return fmt.Errorf("--prompt 是必填参数")
		}
		if createSchedule == "" && createRunAt == "" {
			return fmt.Errorf("需要指定 --schedule 或 --at")
		}

		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskCreate, ipc.CreateTaskParams{
			Name:     createName,
			Prompt:   createPrompt,
			Schedule: createSchedule,
			RunAt:    createRunAt,
		})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}

		data, _ := json.Marshal(resp.Result)
		var result ipc.TaskResult
		json.Unmarshal(data, &result)

		fmt.Printf("✓ Task created\n")
		fmt.Printf("  ID:       %s\n", result.ID)
		fmt.Printf("  Name:     %s\n", result.Name)
		fmt.Printf("  Status:   %s\n", result.Status)
		if result.Schedule != "" {
			fmt.Printf("  Schedule: %s\n", result.Schedule)
		}
		if result.NextRunAt != "" {
			fmt.Printf("  Next run: %s\n", result.NextRunAt)
		}
		return nil
	},
}

func init() {
	createCmd.Flags().StringVarP(&createName, "name", "n", "", "任务名称")
	createCmd.Flags().StringVarP(&createPrompt, "prompt", "p", "", "发送给 Claude Code 的 prompt（必填）")
	createCmd.Flags().StringVarP(&createSchedule, "schedule", "s", "", "cron 表达式（如 '0 9 * * *'）")
	createCmd.Flags().StringVarP(&createRunAt, "at", "a", "", "一次性任务执行时间（格式：YYYY-MM-DDTHH:MM:SS，如 '2026-06-13T14:59:00'）")

	_ = createCmd.MarkFlagRequired("prompt")
}
