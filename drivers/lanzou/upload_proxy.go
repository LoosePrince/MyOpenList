package lanzou

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func (d *LanZou) UploadProxyHash() string { return "" }

func (d *LanZou) PrepareUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	if !d.IsAccount() && !d.IsCookie() {
		return nil, errors.New("Lanzou share mounts cannot upload")
	}
	return &model.UploadProxyPlan{
		URL: strings.TrimRight(d.BaseUrl, "/") + "/html5up.php", Method: http.MethodPost,
		Headers:  map[string]string{"Cookie": d.Cookie, "User-Agent": d.UserAgent, "Referer": "https://pc.woozooo.com"},
		Encoding: "multipart", FileField: "upload_file",
		Fields: map[string]string{"task": "1", "vie": "2", "ve": "2", "id": "WU_FILE_0", "name": req.FileName, "folder_id_bb_n": dir.GetID()},
	}, nil
}

func (d *LanZou) CompleteUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest, plan *model.UploadProxyPlan, result model.UploadProxyResult) (model.Obj, error) {
	var resp struct {
		ZT   int            `json:"zt"`
		Text []FileOrFolder `json:"text"`
	}
	if err := utils.Json.Unmarshal([]byte(result.Body), &resp); err != nil {
		return nil, err
	}
	if (resp.ZT != 1 && resp.ZT != 2) || len(resp.Text) == 0 || resp.Text[0].GetID() == "" {
		return nil, errors.New("Lanzou rejected upload")
	}
	return &resp.Text[0], nil
}

var _ driver.UploadProxyUploader = (*LanZou)(nil)
