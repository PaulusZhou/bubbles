package cli

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "bubbles",
	Short: "Bubbles - Claude Code 定时任务调度工具",
	Long:  "Bubbles 是一个定时调用 Claude Code 执行任务的命令行工具，支持 cron 定时任务和一次性任务。",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(pauseCmd)
	rootCmd.AddCommand(resumeCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(logsCmd)
}
