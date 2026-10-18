package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	archiveDays    int
	archiveBefore  string
	archiveDryRun  bool
	archiveExclude string
)

var archiveCmd = &cobra.Command{
	Use:   "archive <dir>...",
	Short: "按时间归档旧文档到 _archive/YYYY-MM/（可配合 bubbles upload 同步到飞书）",
	Long: `把目录中"过期"的文档移动到 {dir}/_archive/YYYY-MM/ 下（保留相对路径结构）：

  bubbles archive invest-system --days 90          # 归档 90 天前的文档
  bubbles archive invest-system --before 2026-07-01
  bubbles archive invest-system/stocks --dry-run   # 预览

永不归档：policy.yaml、decision-log.md（决策台账）、_archive/ 本身、隐藏文件，
以及 --exclude 匹配的路径（默认排除 SKILL.md、scripts/、references/）。
归档后目录结构会随 bubbles upload 镜像到飞书云文档。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("需要至少一个目录")
		}
		var cutoff time.Time
		if archiveBefore != "" {
			t, err := time.Parse("2006-01-02", archiveBefore)
			if err != nil {
				return fmt.Errorf("--before 需要 YYYY-MM-DD 格式: %w", err)
			}
			cutoff = t
		} else {
			cutoff = time.Now().AddDate(0, 0, -archiveDays)
		}

		excludes := map[string]bool{}
		for _, e := range strings.Split(archiveExclude, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				excludes[e] = true
			}
		}

		var moved, skipped int
		for _, target := range args {
			base, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			info, err := os.Stat(base)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("archive 需要目录参数: %s", base)
			}
			err = filepath.Walk(base, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(base, path)
				if fi.IsDir() {
					name := fi.Name()
					if path != base && (strings.HasPrefix(name, ".") || name == "_archive" || excludes[name+"/"] || excludes[name]) {
						return filepath.SkipDir
					}
					return nil
				}
				name := fi.Name()
				if strings.HasPrefix(name, ".") || name == "policy.yaml" || name == "decision-log.md" {
					return nil
				}
				for ex := range excludes {
					if strings.HasSuffix(ex, "/") && strings.Contains(rel+string(filepath.Separator), string(filepath.Separator)+ex) {
						return nil
					}
					if !strings.HasSuffix(ex, "/") && (rel == ex || name == ex) {
						return nil
					}
				}
				if fi.ModTime().After(cutoff) {
					skipped++
					return nil
				}
				month := fi.ModTime().Format("2006-01")
				dest := filepath.Join(base, "_archive", month, rel)
				if archiveDryRun {
					fmt.Printf("[dry-run] %s → %s\n", path, dest)
					moved++
					return nil
				}
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					return err
				}
				if err := os.Rename(path, dest); err != nil {
					// 跨设备等场景回退到复制删除
					if cerr := copyFile(path, dest); cerr != nil {
						return err
					}
					os.Remove(path)
				}
				fmt.Printf("✓ %s → %s\n", path, dest)
				moved++
				return nil
			})
			if err != nil {
				return err
			}
		}
		verb := "已归档"
		if archiveDryRun {
			verb = "将归档"
		}
		fmt.Printf("✓ %s %d 个文件，保留 %d 个（截止 %s）\n", verb, moved, skipped, cutoff.Format("2006-01-02"))
		return nil
	},
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func init() {
	archiveCmd.Flags().IntVar(&archiveDays, "days", 90, "归档 N 天前的文档")
	archiveCmd.Flags().StringVar(&archiveBefore, "before", "", "归档该日期（YYYY-MM-DD）之前的文档，优先于 --days")
	archiveCmd.Flags().BoolVar(&archiveDryRun, "dry-run", false, "只显示将执行的动作")
	archiveCmd.Flags().StringVar(&archiveExclude, "exclude", "SKILL.md,scripts/,references/", "额外排除的文件/目录（逗号分隔，目录以 / 结尾）")
}
