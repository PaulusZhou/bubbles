package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var pauseCmd = &cobra.Command{
	Use:   "pause <task-id>",
	Short: "暂停任务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskPause, ipc.TaskIDParams{ID: args[0]})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}
		fmt.Printf("✓ Task %s paused\n", args[0])
		return nil
	},
}
