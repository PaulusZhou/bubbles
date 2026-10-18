package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	drivev1 "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

// DriveUploader uploads local files to Feishu Drive (云文档).
// userToken 为空时用应用身份（tenant，文件落应用空间）；
// 非空时用用户身份（文件落用户“我的空间”，用户直接可见）。
type DriveUploader struct {
	client  *lark.Client
	userTok string
}

func NewDriveUploader(appID, appSecret string) *DriveUploader {
	return &DriveUploader{client: lark.NewClient(appID, appSecret)}
}

// NewDriveUploaderWithUser 以用户身份上传。
func NewDriveUploaderWithUser(appID, appSecret, userToken string) *DriveUploader {
	return &DriveUploader{client: lark.NewClient(appID, appSecret), userTok: userToken}
}

func (u *DriveUploader) opts() []larkcore.RequestOptionFunc {
	if u.userTok != "" {
		return []larkcore.RequestOptionFunc{larkcore.WithUserAccessToken(u.userTok)}
	}
	return nil
}

// UploadFile uploads one local file into a Drive folder. Returns the new file token and URL.
func (u *DriveUploader) UploadFile(localPath, folderToken string) (string, string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", "", err
	}

	body := drivev1.NewUploadAllFileReqBodyBuilder().
		FileName(filepath.Base(localPath)).
		ParentType("explorer").
		ParentNode(folderToken).
		Size(int(st.Size())).
		File(f).
		Build()
	req := drivev1.NewUploadAllFileReqBuilder().Body(body).Build()
	resp, err := u.client.Drive.File.UploadAll(context.Background(), req, u.opts()...)
	if err != nil {
		return "", "", fmt.Errorf("upload %s: %w", localPath, err)
	}
	if !resp.Success() {
		return "", "", fmt.Errorf("upload %s: code=%d msg=%s", localPath, resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.FileToken == nil {
		return "", "", fmt.Errorf("upload %s: empty response", localPath)
	}
	url := ""
	if resp.Data.Url != nil {
		url = *resp.Data.Url
	}
	return *resp.Data.FileToken, url, nil
}

func sp(s string) *string { return &s }

// ImportMarkdown 把本地 markdown 导入为飞书原生文档（docx）。
// 文档可直接在飞书中打开编辑（而非文件预览）。返回文档 token 与 URL。
func (u *DriveUploader) ImportMarkdown(localPath, folderToken string) (string, string, error) {
	ctx := context.Background()
	// 1) 上传待导入文件（普通 files/upload_all；导入任务的 file_token 即其 Drive FileToken）
	srcToken, _, err := u.UploadFile(localPath, folderToken)
	if err != nil {
		return "", "", fmt.Errorf("导入源文件上传失败: %w", err)
	}

	// 2) 创建导入任务（md → docx）
	name := strings.TrimSuffix(filepath.Base(localPath), filepath.Ext(localPath))
	it := &drivev1.ImportTask{
		FileExtension: sp("md"),
		FileToken:     sp(srcToken),
		Type:          sp("docx"),
		FileName:      sp(name),
		Point: drivev1.NewImportTaskMountPointBuilder().
			MountType(1).
			MountKey(folderToken).
			Build(),
	}
	cResp, err := u.client.Drive.ImportTask.Create(ctx, drivev1.NewCreateImportTaskReqBuilder().ImportTask(it).Build(), u.opts()...)
	if err != nil || !cResp.Success() || cResp.Data == nil || cResp.Data.Ticket == nil {
		return "", "", fmt.Errorf("创建导入任务失败: %v code=%d msg=%s", err, cResp.Code, cResp.Msg)
	}

	// 3) 轮询结果（最多 60s）
	for i := 0; i < 30; i++ {
		gResp, err := u.client.Drive.ImportTask.Get(ctx, drivev1.NewGetImportTaskReqBuilder().Ticket(*cResp.Data.Ticket).Build(), u.opts()...)
		if err != nil || !gResp.Success() || gResp.Data == nil || gResp.Data.Result == nil {
			return "", "", fmt.Errorf("查询导入任务失败: %v", err)
		}
		r := gResp.Data.Result
		if r.JobStatus != nil {
			switch *r.JobStatus {
			case 0: // 成功
				var tok, url string
				if r.Token != nil {
					tok = *r.Token
				}
				if r.Url != nil {
					url = *r.Url
				}
				return tok, url, nil
			case 1, 2: // 初始化/处理中
				time.Sleep(2 * time.Second)
				continue
			default:
				errMsg := ""
				if r.JobErrorMsg != nil {
					errMsg = *r.JobErrorMsg
				}
				return "", "", fmt.Errorf("导入失败 job_status=%d: %s", *r.JobStatus, errMsg)
			}
		}
	}
	return "", "", fmt.Errorf("导入任务超时")
}

// CreateFolder creates a subfolder under parentToken. Returns the folder token.
func (u *DriveUploader) CreateFolder(name, parentToken string) (string, error) {
	body := drivev1.NewCreateFolderFileReqBodyBuilder().
		Name(name).
		FolderToken(parentToken).
		Build()
	req := drivev1.NewCreateFolderFileReqBuilder().Body(body).Build()
	resp, err := u.client.Drive.File.CreateFolder(context.Background(), req, u.opts()...)
	if err != nil {
		return "", fmt.Errorf("create folder %s: %w", name, err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create folder %s: code=%d msg=%s", name, resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.Token == nil {
		return "", fmt.Errorf("create folder %s: empty response", name)
	}
	return *resp.Data.Token, nil
}

// DeleteFile removes a file or doc by token (typ: "file" / "docx" / "folder").
func (u *DriveUploader) DeleteFile(fileToken, typ string) error {
	if typ == "" {
		typ = "file"
	}
	req := drivev1.NewDeleteFileReqBuilder().
		FileToken(fileToken).
		Type(typ).
		Build()
	resp, err := u.client.Drive.File.Delete(context.Background(), req, u.opts()...)
	if err != nil {
		return fmt.Errorf("delete %s: %w", fileToken, err)
	}
	if !resp.Success() {
		// 1061007: 文件已删除 —— 幂等容忍
		if resp.Code == 1061007 {
			return nil
		}
		return fmt.Errorf("delete %s: code=%d msg=%s", fileToken, resp.Code, resp.Msg)
	}
	return nil
}
