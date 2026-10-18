package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/feishu"
)

var (
	uploadFolder string
	uploadExts   string
	uploadDryRun bool
	uploadUser   bool
	uploadDocx   bool
)

// driveState tracks uploaded files/dirs so re-sync skips unchanged files and
// replaces changed ones. ~/.bubbles/feishu-drive-state.json
type driveState struct {
	Files map[string]driveFileEntry `json:"files"`
	Dirs  map[string]string         `json:"dirs"` // local abs dir -> remote folder token
}

type driveFileEntry struct {
	Token    string    `json:"token"`
	Url      string    `json:"url,omitempty"`
	Kind     string    `json:"kind,omitempty"` // file | docx
	ModTime  time.Time `json:"mod_time"`
	Uploaded time.Time `json:"uploaded"`
}

func driveStatePath() string {
	cfg, _ := config.Load()
	dir := cfg.DataDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".bubbles")
	}
	return filepath.Join(dir, "feishu-drive-state.json")
}

func loadDriveState() *driveState {
	s := &driveState{Files: map[string]driveFileEntry{}, Dirs: map[string]string{}}
	raw, err := os.ReadFile(driveStatePath())
	if err == nil {
		json.Unmarshal(raw, s)
	}
	if s.Files == nil {
		s.Files = map[string]driveFileEntry{}
	}
	if s.Dirs == nil {
		s.Dirs = map[string]string{}
	}
	return s
}

func (s *driveState) save() error {
	raw, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(driveStatePath(), raw, 0644)
}

var uploadCmd = &cobra.Command{
	Use:   "upload <file|dir>...",
	Short: "上传文档到飞书云文档（目录递归镜像，增量同步）",
	Long: `把本地文档上传到飞书云文档文件夹：

  bubbles upload invest-system                    # 目录递归（镜像子目录结构）
  bubbles upload decision-log.md policy.yaml      # 指定文件
  bubbles upload invest-system --folder fldXXXX   # 指定目标文件夹（覆盖配置）

增量同步：未变化跳过；变化的删除旧文件后重传；目录结构在云文档侧自动创建。
目标文件夹取 --folder > 配置 feishu_drive_folder > 环境 FEISHU_DRIVE_FOLDER。

身份：--user 用你的飞书身份上传（文件进「我的空间」，你自己可见，需先 bubbles login）；
不加 --user 用应用身份（文件在应用空间，只有机器人可见）。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("需要至少一个文件或目录")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.FeishuAppID == "" || cfg.FeishuAppSecret == "" {
			return fmt.Errorf("缺少 FEISHU_APP_ID/FEISHU_APP_SECRET")
		}
		root := uploadFolder
		if root == "" {
			root = cfg.FeishuDriveFolder
		}
		exts := map[string]bool{}
		for _, e := range strings.Split(uploadExts, ",") {
			e = strings.TrimSpace(strings.ToLower(e))
			if e != "" {
				if !strings.HasPrefix(e, ".") {
					e = "." + e
				}
				exts[e] = true
			}
		}

		state := loadDriveState()
		var up *feishu.DriveUploader
		if uploadUser {
			tok, err := feishu.LoadUserToken()
			if err != nil {
				return err
			}
			ut, err := tok.EnsureValid(cfg.FeishuAppID, cfg.FeishuAppSecret)
			if err != nil {
				return err
			}
			up = feishu.NewDriveUploaderWithUser(cfg.FeishuAppID, cfg.FeishuAppSecret, ut)
		} else {
			up = feishu.NewDriveUploader(cfg.FeishuAppID, cfg.FeishuAppSecret)
		}
		if root == "" {
			if !uploadUser {
				return fmt.Errorf("缺少目标文件夹：--folder 或配置 feishu_drive_folder（应用身份下你可能看不到文件，建议加 --user）")
			}
			// 用户身份且未指定：在我的空间自动创建/复用「投资文档」文件夹
			const key = "__myspace__/投资文档"
			if t, ok := state.Dirs[key]; ok {
				root = t
			} else {
				t, err := up.CreateFolder("投资文档", "")
				if err != nil {
					return err
				}
				state.Dirs[key] = t
				root = t
			}
			fmt.Printf("ℹ 我的空间文件夹「投资文档」：%s\n", root)
		}

		var uploaded, skipped, replaced int
		for _, target := range args {
			abs, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			info, err := os.Stat(abs)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				n, sk, rp, err := syncFile(up, state, abs, root, uploadDocx)
				if err != nil {
					return err
				}
				uploaded += n
				skipped += sk
				replaced += rp
				continue
			}
			err = filepath.Walk(abs, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if fi.IsDir() {
					if strings.HasPrefix(fi.Name(), ".") && path != abs {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(fi.Name(), ".") || !exts[strings.ToLower(filepath.Ext(path))] {
					return nil
				}
				rel, _ := filepath.Rel(abs, path)
				remoteDir := filepath.Join(filepath.Dir(rel))
				folderToken, err := ensureDir(up, state, root, abs, remoteDir, uploadDryRun)
				if err != nil {
					return err
				}
				n, sk, rp, err := syncFile(up, state, path, folderToken, uploadDocx)
				if err != nil {
					return err
				}
				uploaded += n
				skipped += sk
				replaced += rp
				return nil
			})
			if err != nil {
				return err
			}
		}
		if !uploadDryRun {
			if err := state.save(); err != nil {
				fmt.Printf("⚠ 状态文件写入失败: %v\n", err)
			}
		}
		fmt.Printf("✓ 同步完成：新传 %d，替换 %d，跳过 %d（目标 %s）\n", uploaded, replaced, skipped, root)
		return nil
	},
}

// ensureDir mirrors a relative directory tree in Drive, returns folder token.
func ensureDir(up *feishu.DriveUploader, state *driveState, root, base, relDir string, dry bool) (string, error) {
	if relDir == "." || relDir == "" {
		return root, nil
	}
	cur := base
	token := root
	for _, part := range strings.Split(relDir, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dkey := root + "|" + cur
		if t, ok := state.Dirs[dkey]; ok {
			token = t
			continue
		}
		if dry {
			fmt.Printf("[dry-run] 创建文件夹 %s\n", cur)
			token = "dry-run"
			continue
		}
		t, err := up.CreateFolder(part, token)
		if err != nil {
			return "", err
		}
		state.Dirs[dkey] = t
		token = t
	}
	return token, nil
}

// doUpload 上传或导入一个文件，返回 token/url/kind。
func doUpload(up *feishu.DriveUploader, path, folderToken string, useDocx bool) (string, string, string, error) {
	if useDocx && strings.EqualFold(filepath.Ext(path), ".md") {
		tok, url, err := up.ImportMarkdown(path, folderToken)
		return tok, url, "docx", err
	}
	tok, url, err := up.UploadFile(path, folderToken)
	return tok, url, "file", err
}

func syncFile(up *feishu.DriveUploader, state *driveState, path, folderToken string, useDocx bool) (uploaded, skipped, replaced int, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	key := folderToken + "|" + path
	prev, known := state.Files[key]
	if known && !prev.ModTime.Before(fi.ModTime()) {
		return 0, 1, 0, nil // unchanged
	}
	if uploadDryRun {
		verb := "新传"
		if known {
			verb = "替换"
		}
		fmt.Printf("[dry-run] %s %s\n", verb, path)
		if known {
			return 0, 0, 1, nil
		}
		return 1, 0, 0, nil
	}
	if known && prev.Token != "" {
		typ := prev.Kind
		if typ == "" {
			typ = "file"
		}
		if derr := up.DeleteFile(prev.Token, typ); derr != nil {
			fmt.Printf("⚠ 旧文档删除失败（继续上传新版）: %v\n", derr)
		}
		token, url, kind, uerr := doUpload(up, path, folderToken, useDocx)
		if uerr != nil {
			return 0, 0, 0, uerr
		}
		state.Files[key] = driveFileEntry{Token: token, Url: url, Kind: kind, ModTime: fi.ModTime(), Uploaded: time.Now()}
		fmt.Printf("  🔗 %s\n", url)
		return 0, 0, 1, nil
	}
	token, url, kind, uerr := doUpload(up, path, folderToken, useDocx)
	if uerr != nil {
		return 0, 0, 0, uerr
	}
	state.Files[key] = driveFileEntry{Token: token, Url: url, Kind: kind, ModTime: fi.ModTime(), Uploaded: time.Now()}
	fmt.Printf("  🔗 %s\n", url)
	return 1, 0, 0, nil
}

func init() {
	uploadCmd.SilenceUsage = true
	uploadCmd.Flags().StringVar(&uploadFolder, "folder", "", "目标云文档文件夹 token（默认取配置 feishu_drive_folder）")
	uploadCmd.Flags().StringVar(&uploadExts, "ext", ".md,.txt,.yaml,.yml,.json,.csv", "包含的扩展名（逗号分隔）")
	uploadCmd.Flags().BoolVar(&uploadDryRun, "dry-run", false, "只显示将执行的动作，不实际上传")
	uploadCmd.Flags().BoolVar(&uploadUser, "user", false, "以你的飞书身份上传（进我的空间；需先 bubbles login）")
	uploadCmd.Flags().BoolVar(&uploadDocx, "docx", false, "把 .md 导入为飞书原生文档（可直接打开编辑，非文件预览）")
}
