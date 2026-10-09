package guangyapan

import (
	"context"
	"errors"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// MD5 is optional for GuangYaPan. Omitting it keeps the upload streaming.
func (d *GuangYaPan) UploadProxyHash() string { return "" }

func (d *GuangYaPan) PrepareUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	if err := d.ensureAccessToken(ctx); err != nil {
		return nil, err
	}
	token, code, err := d.getUploadToken(ctx, dir.GetID(), req.FileName, req.FileSize, "")
	if err != nil {
		return nil, err
	}
	plan := &model.UploadProxyPlan{Method: "PUT", State: token.TaskID}
	if code == 156 || token.AlreadyDone {
		plan.Instant = true
		return plan, nil
	}
	if token.ObjectPath == "" || token.BucketName == "" || token.EndPoint == "" || token.AccessKeyID == "" || token.SecretAccessKey == "" {
		return nil, errors.New("incomplete OSS upload authorization")
	}
	client, err := oss.New(normalizeOSSEndpoint(token.EndPoint, token.BucketName), token.AccessKeyID, token.SecretAccessKey, oss.SecurityToken(token.SessionToken))
	if err != nil {
		return nil, err
	}
	bucket, err := client.Bucket(token.BucketName)
	if err != nil {
		return nil, err
	}
	plan.URL, err = bucket.SignURL(token.ObjectPath, oss.HTTPPut, 900, oss.ContentType(req.ContentType))
	plan.Headers = map[string]string{"Content-Type": req.ContentType}
	return plan, err
}

func (d *GuangYaPan) CompleteUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest, plan *model.UploadProxyPlan, result model.UploadProxyResult) (model.Obj, error) {
	taskID := plan.State.(string)
	if err := d.waitUploadTaskInfo(ctx, taskID); err != nil {
		return nil, err
	}
	var out taskInfoResp
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/file/get_info_by_task_id", map[string]any{"taskId": taskID}, &out); err != nil {
		return nil, err
	}
	if out.Data.FileID == "" {
		return nil, errors.New("upload task contains no file id")
	}
	// Resolve the provider's chosen name before applying overwrite handling.
	files, err := d.List(ctx, dir, model.ListArgs{Refresh: true})
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.GetID() == out.Data.FileID {
			return file, nil
		}
	}
	return nil, errors.New("uploaded file is not yet visible in the destination directory")
}

var _ driver.UploadProxyUploader = (*GuangYaPan)(nil)
