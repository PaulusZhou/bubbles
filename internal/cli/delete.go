package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var deleteCmd = &cobra.Command{
	Use:   "delete <task-id>",
	Short: "删除任务",
	Aliases: []string{"rm", "del"},
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodTaskDelete, ipc.TaskIDParams{ID: args[0]})
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return fmt.Errorf(resp.Error)
		}
		fmt.Printf("✓ Task %s deleted\n", args[0])
		return nil
	},
}
