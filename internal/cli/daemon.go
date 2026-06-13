package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/ipc"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "管理守护进程",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "启动守护进程",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := daemon.Start(); err != nil {
			return err
		}
		fmt.Println("✓ Daemon started")
		return nil
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止守护进程",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := daemon.Stop(); err != nil {
			return err
		}
		fmt.Println("✓ Daemon stopped")
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看守护进程状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := ipc.NewClient(daemon.SocketPath())
		resp, err := client.Call(ipc.MethodDaemonStatus, nil)
		if err != nil {
			// daemon 未运行
			running, pid := daemon.IsRunning()
			if running {
				fmt.Printf("Daemon running (PID: %d) but IPC not responding\n", pid)
			} else {
				fmt.Println("Daemon is not running")
			}
			return nil
		}

		data, _ := json.Marshal(resp.Result)
		var status ipc.DaemonStatusResult
		json.Unmarshal(data, &status)

		if status.Running {
			fmt.Printf("Daemon running\n")
			fmt.Printf("  PID:      %d\n", status.PID)
			fmt.Printf("  Uptime:   %s\n", status.Uptime)
			fmt.Printf("  Tasks:    %d total, %d active\n", status.TaskCount, status.ActiveCount)
		} else {
			fmt.Println("Daemon is not running")
		}
		return nil
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
}
