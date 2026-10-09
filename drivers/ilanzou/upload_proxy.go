package ilanzou

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
	"github.com/go-resty/resty/v2"
)

func (d *ILanZou) UploadProxyHash() string { return "md5" }

func (d *ILanZou) PrepareUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	data, err := d.proved("/7n/getUpToken", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx).SetBody(base.Json{"fileId": "", "fileName": req.FileName, "fileSize": max((req.FileSize+1023)/1024, 1), "folderId": dir.GetID(), "md5": req.Hash, "type": 1})
	})
	if err != nil {
		return nil, err
	}
	token := utils.Json.Get(data, "upToken").ToString()
	if token == "-1" {
		var resp UploadTokenRapidResp
		if err = utils.Json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		if resp.Map.FileID <= 0 || resp.Map.FileName == "" {
			return nil, errors.New("iLanzou instant upload contains no file metadata")
		}
		return &model.UploadProxyPlan{Instant: true, State: &model.Object{ID: strconv.FormatInt(resp.Map.FileID, 10), Name: resp.Map.FileName, Size: req.FileSize, Modified: time.Now()}}, nil
	}
	if token == "" {
		return nil, errors.New("empty Qiniu upload token")
	}
	now := time.Now()
	key := fmt.Sprintf("disk/%04d/%02d/%02d/%s/%d-%s.rar", now.Year(), now.Month(), now.Day(), d.account, now.UnixMilli(), random.String(8))
	return &model.UploadProxyPlan{URL: "https://upload.qiniup.com/", Method: "POST", Encoding: "multipart", FileField: "file", Fields: map[string]string{"token": token, "key": key, "fname": req.FileName}}, nil
}

func (d *ILanZou) CompleteUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest, plan *model.UploadProxyPlan, result model.UploadProxyResult) (model.Obj, error) {
	if plan.Instant {
		return plan.State.(model.Obj), nil
	}
	token := utils.Json.Get([]byte(result.Body), "token").ToString()
	if token == "" {
		return nil, errors.New("Qiniu response contains no result token")
	}
	var resp UploadResultResp
	for i := 0; i < maxUploadCommitRetries; i++ {
		_, err := d.unproved("/7n/results", http.MethodPost, func(r *resty.Request) {
			r.SetContext(ctx).SetQueryParams(map[string]string{"tokenList": token, "tokenTime": time.Now().Format("Mon Jan 02 2006 15:04:05 GMT-0700 (MST)")}).SetResult(&resp)
		})
		if err != nil {
			return nil, err
		}
		if len(resp.List) > 0 && resp.List[0].Status == 1 {
			file := resp.List[0]
			if file.FileId <= 0 || file.FileName == "" {
				return nil, errors.New("iLanzou upload commit contains no file metadata")
			}
			return &model.Object{ID: strconv.FormatInt(file.FileId, 10), Name: file.FileName, Size: req.FileSize, Modified: time.Now(), HashInfo: utils.NewHashInfo(utils.MD5, req.Hash)}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(uploadCommitRetryDelay):
		}
	}
	return nil, errors.New("iLanzou upload commit did not complete")
}

var _ driver.UploadProxyUploader = (*ILanZou)(nil)
