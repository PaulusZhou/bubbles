package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var runCmd = &cobra.Command{
	Use:   "run <task-id>",
	Short: "立即执行一次任务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskRun, ipc.TaskIDParams{ID: args[0]})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}
		fmt.Printf("✓ Task %s triggered\n", args[0])
		return nil
	},
}
