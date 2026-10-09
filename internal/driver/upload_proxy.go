package driver

import (
	"context"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// Embed only in additions for drivers that implement UploadProxyUploader.
// Existing administration clients render these fields from driver metadata.
type UploadProxy struct {
	UploadProxyURL         string `json:"upload_proxy_url" help:"Upload proxy URL; leave empty to disable"`
	DisableUploadProxySign bool   `json:"disable_upload_proxy_sign" default:"false" help:"Allow anonymous unsigned upload through the upload proxy"`
}

func (p *UploadProxy) GetUploadProxyConfig() *UploadProxy { return p }

type UploadProxyUploader interface {
	GetUploadProxyConfig() *UploadProxy
	UploadProxyHash() string
	PrepareUploadProxy(context.Context, model.Obj, model.UploadProxyRequest) (*model.UploadProxyPlan, error)
	CompleteUploadProxy(context.Context, model.Obj, model.UploadProxyRequest, *model.UploadProxyPlan, model.UploadProxyResult) (model.Obj, error)
}
