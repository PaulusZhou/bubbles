package cli

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/feishu"
)

var loginPort int
var loginTimeout time.Duration

func init() { loginCmd.SilenceUsage = true }

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "以你本人的飞书身份登录（OAuth），让上传文档落在你自己的云文档",
	Long: `bubbles login — 用户身份授权（一次性，token 自动续期）：

1. 先在飞书开放平台「安全设置 → 重定向 URL」登记: http://localhost:18989/callback
2. 运行本命令，浏览器打开授权页并确认
3. 之后 bubbles upload --user 上传的文档会出现在你自己的云文档「我的空间」

未登录时 bubbles upload 默认仍走应用身份（文件在应用空间，你看不到）。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.FeishuAppID == "" || cfg.FeishuAppSecret == "" {
			return fmt.Errorf("缺少 FEISHU_APP_ID/FEISHU_APP_SECRET")
		}
		redirectURI := fmt.Sprintf("http://localhost:%d/callback", loginPort)

		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", loginPort)) // 双栈：兼容 localhost 解析到 IPv6(::1) 的浏览器
		if err != nil {
			return fmt.Errorf("本地端口 %d 监听失败: %w", loginPort, err)
		}
		defer ln.Close()

		codeCh := make(chan string, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code == "" {
				w.WriteHeader(400)
				fmt.Fprint(w, "缺少 code 参数")
				return
			}
			fmt.Fprint(w, "✅ 授权成功，可关闭此页面回到终端。")
			codeCh <- code
		})
		go http.Serve(ln, mux)

		authURL := feishu.AuthorizeURL(cfg.FeishuAppID, redirectURI)
		fmt.Println("▶ 请在浏览器中完成飞书授权：")
		fmt.Println("  " + authURL)
		openBrowser(authURL)
		fmt.Println("\n（浏览器没自动打开的话，手动复制上面的链接）")
		fmt.Println("⚠ 若报 redirect_uri 错误：先到开放平台「安全设置 → 重定向 URL」登记 " + redirectURI)

		var code string
		select {
		case code = <-codeCh:
		case <-time.After(loginTimeout):
			return fmt.Errorf("等待授权超时（%s）", loginTimeout)
		}

		tok, err := feishu.ExchangeCode(cfg.FeishuAppID, cfg.FeishuAppSecret, code, redirectURI)
		if err != nil {
			return err
		}
		if err := tok.Save(); err != nil {
			return err
		}
		fmt.Printf("✅ 登录成功！user token 已保存（有效期至 %s，自动续期）\n", tok.ExpiresAt.Format("2006-01-02 15:04"))
		fmt.Println("现在可用：bubbles upload <文件或目录> --user   # 文档进入你自己的云文档")
		return nil
	},
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func init() {
	loginCmd.Flags().IntVar(&loginPort, "port", 18989, "本地回调端口（需与开放平台登记的重定向 URL 一致）")
	loginCmd.Flags().DurationVar(&loginTimeout, "timeout", 30*time.Minute, "等待授权的时长")
}
