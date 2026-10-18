package feishu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UserToken 是"以用户身份"访问飞书的凭据（OAuth 授权码流程）。
// 用 user token 上传的文档落在用户自己的云文档（我的空间），用户直接可见；
// 应用身份（tenant token）上传的文档在应用空间，用户不可见。
type UserToken struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func userTokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".bubbles", "feishu-user-token.json")
}

func LoadUserToken() (*UserToken, error) {
	raw, err := os.ReadFile(userTokenPath())
	if err != nil {
		return nil, fmt.Errorf("未登录（%v），先执行 bubbles login", err)
	}
	var t UserToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("token 文件损坏，请重新 bubbles login: %w", err)
	}
	return &t, nil
}

func (t *UserToken) Save() error {
	if err := os.MkdirAll(filepath.Dir(userTokenPath()), 0755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(t, "", "  ")
	return os.WriteFile(userTokenPath(), raw, 0600)
}

// Valid 判断 access_token 是否仍有效（留 60s 余量）。
func (t *UserToken) Valid() bool {
	return t.AccessToken != "" && time.Now().Before(t.ExpiresAt.Add(-60*time.Second))
}

// AuthorizeURL 生成用户授权页地址（携带云文档所需 scope）。
// 需先在飞书开放平台「安全设置 → 重定向 URL」登记 redirectURI。
func AuthorizeURL(appID, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", appID)
	q.Set("app_id", appID) // 兼容旧参数名
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join([]string{
		"offline_access",             // refresh_token 自动续期
		"drive:file:upload",          // 上传文件（官方文档 scope）
		"space:folder:create",        // 创建文件夹（目录镜像）
		"space:document:delete",      // 删除/替换旧版（官方文档 scope，注意不是 drive:file:delete）
		"space:document:retrieve",    // 列目录/验收（官方文档 scope）
		"docs:document:import",       // md 导入为飞书原生文档 docx（官方文档 scope）
		"docs:document.media:upload", // 导入素材上传（medias/upload_all 接口点名的 scope）
	}, " "))
	q.Set("state", fmt.Sprintf("bubbles-%d", time.Now().Unix()))
	return "https://accounts.feishu.cn/open-apis/authen/v1/authorize?" + q.Encode()
}

type oauthTokenResp struct {
	Code             int    `json:"code"`
	Msg              string `json:"msg"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func postOAuth(appID, appSecret string, form url.Values) (*UserToken, error) {
	resp, err := http.PostForm("https://open.feishu.cn/open-apis/authen/v2/oauth/token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r oauthTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}
	if r.AccessToken == "" {
		return nil, fmt.Errorf("获取 user token 失败: code=%d msg=%s error=%s %s",
			r.Code, r.Msg, r.Error, r.ErrorDescription)
	}
	return &UserToken{
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		ExpiresAt:        time.Now().Add(time.Duration(r.ExpiresIn) * time.Second),
		RefreshExpiresAt: time.Now().Add(time.Duration(r.RefreshExpiresIn) * time.Second),
	}, nil
}

// ExchangeCode 把授权码换成 user token。
func ExchangeCode(appID, appSecret, code, redirectURI string) (*UserToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", appID)
	form.Set("client_secret", appSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	return postOAuth(appID, appSecret, form)
}

// Refresh 用 refresh_token 续期，失败时返回错误（需重新登录）。
func (t *UserToken) Refresh(appID, appSecret string) error {
	if t.RefreshToken == "" || time.Now().After(t.RefreshExpiresAt) {
		return fmt.Errorf("refresh_token 已失效，请重新 bubbles login")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", appID)
	form.Set("client_secret", appSecret)
	form.Set("refresh_token", t.RefreshToken)
	nt, err := postOAuth(appID, appSecret, form)
	if err != nil {
		return err
	}
	*t = *nt
	return t.Save()
}

// EnsureValid 返回可用的 access_token，必要时自动续期。
func (t *UserToken) EnsureValid(appID, appSecret string) (string, error) {
	if t.Valid() {
		return t.AccessToken, nil
	}
	if err := t.Refresh(appID, appSecret); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// NormalizeRedirectURI 统一 localhost/127.0.0.1 差异（注册时二选一，回调时都试）。
func NormalizeRedirectURI(s string) string {
	return strings.TrimSpace(s)
}
