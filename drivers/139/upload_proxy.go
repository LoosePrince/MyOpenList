package _139

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
)

type proxyUploadState struct {
	FileID       string
	Name         string
	UploadID     string
	CompletePath string
}

func (d *Yun139) UploadProxyHash() string {
	if d.Type == MetaPersonalNew || ((d.isFamily() || d.isGroup()) && !d.UseOldStreamUpload) {
		return "sha256"
	}
	return ""
}

func (d *Yun139) PrepareUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	if d.isShare() {
		return nil, errors.New("139 share mounts cannot upload")
	}
	if d.UploadProxyHash() == "sha256" {
		createPath, completePath := "/file/create", "/file/complete"
		data := base.Json{"contentHash": req.Hash, "contentHashAlgorithm": "SHA256", "contentType": req.ContentType, "parallelUpload": false,
			"partInfos": []PartInfo{{PartNumber: 1, PartSize: req.FileSize}}, "size": req.FileSize,
			"parentFileId": dir.GetID(), "name": req.FileName, "type": "file", "fileRenameMode": "auto_rename"}
		if d.isFamily() || d.isGroup() {
			if d.CloudID == "" {
				return nil, errors.New("cloud_id is required")
			}
			createPath, completePath = "/dynamic/file/create", "/dynamic/file/complete"
			data["groupId"], data["catalogType"], data["seqNo"] = d.CloudID, 3, random.String(32)
			data["groupType"] = 1
			if d.isGroup() {
				data["groupType"] = 2
			}
		}
		var resp PersonalUploadResp
		if _, err := d.newPost(createPath, data, &resp); err != nil {
			return nil, err
		}
		state := proxyUploadState{FileID: resp.Data.FileId, Name: resp.Data.FileName, UploadID: resp.Data.UploadId, CompletePath: completePath}
		plan := &model.UploadProxyPlan{Method: "PUT", State: state, Headers: map[string]string{"Content-Type": "application/octet-stream", "Origin": "https://yun.139.com", "Referer": "https://yun.139.com/"}}
		if resp.Data.Exist || (resp.Data.RapidUpload && len(resp.Data.PartInfos) == 0) {
			plan.Instant = true
			return plan, nil
		}
		if len(resp.Data.PartInfos) != 1 || resp.Data.PartInfos[0].PartNumber != 1 || resp.Data.PartInfos[0].UploadUrl == "" {
			return nil, errors.New("139 did not authorize a single-request upload")
		}
		plan.URL = resp.Data.PartInfos[0].UploadUrl
		return plan, nil
	}
	if d.Type != MetaPersonal && !d.isFamily() && !d.isGroup() {
		return nil, errors.New("unsupported 139 upload mode")
	}
	if req.FileSize == 0 {
		return nil, errors.New("139 legacy upload proxy does not support empty files")
	}
	size := req.FileSize
	if !d.ReportRealSize {
		size = 0
	}
	data := base.Json{"manualRename": 2, "operation": 0, "fileCount": 1, "totalSize": size,
		"uploadContentList": []base.Json{{"contentName": req.FileName, "contentSize": size}}, "parentCatalogID": dir.GetID(), "newCatalogName": "",
		"commonAccountInfo": base.Json{"account": d.getAccount(), "accountType": 1}}
	endpoint := "/orchestration/personalCloud/uploadAndDownload/v1.0/pcUploadFileRequest"
	if d.isFamily() || d.isGroup() {
		uploadPath := d.dirPath(dir)
		if d.isGroup() && dir.GetID() == d.RootFolderID {
			uploadPath = "0"
		}
		data = d.newJson(base.Json{"fileCount": 1, "manualRename": 2, "operation": 0, "path": uploadPath, "seqNo": random.String(32), "totalSize": size,
			"uploadContentList": []base.Json{{"contentName": req.FileName, "contentSize": size}}})
		endpoint = "/orchestration/familyCloud-rebuild/content/v1.0/getFileUploadURL"
	}
	var resp UploadResp
	if _, err := d.post(endpoint, data, &resp); err != nil {
		return nil, err
	}
	if resp.Data.Result.ResultCode != "0" {
		return nil, fmt.Errorf("139 rejected upload initialization: %s", resp.Data.Result.ResultCode)
	}
	if len(resp.Data.UploadResult.NewContentIDList) != 1 || resp.Data.UploadResult.NewContentIDList[0].ContentID == "" || resp.Data.UploadResult.NewContentIDList[0].ContentName == "" {
		return nil, errors.New("139 legacy upload contains no file metadata")
	}
	file := resp.Data.UploadResult.NewContentIDList[0]
	return &model.UploadProxyPlan{URL: resp.Data.UploadResult.RedirectionURL, Method: "POST",
		State: proxyUploadState{FileID: file.ContentID, Name: file.ContentName},
		Headers: map[string]string{"Content-Type": "text/plain;name=" + unicode(req.FileName), "contentSize": strconv.FormatInt(req.FileSize, 10),
			"range": fmt.Sprintf("bytes=0-%d", req.FileSize-1), "uploadtaskID": resp.Data.UploadResult.UploadTaskID, "rangeType": "0"}}, nil
}

func (d *Yun139) CompleteUploadProxy(ctx context.Context, dir model.Obj, req model.UploadProxyRequest, plan *model.UploadProxyPlan, result model.UploadProxyResult) (model.Obj, error) {
	if state, ok := plan.State.(proxyUploadState); ok && state.CompletePath != "" {
		if !plan.Instant {
			data := base.Json{"contentHash": req.Hash, "contentHashAlgorithm": "SHA256", "fileId": state.FileID, "uploadId": state.UploadID}
			if d.isFamily() || d.isGroup() {
				data["groupId"] = d.CloudID
			}
			if _, err := d.newPost(state.CompletePath, data, nil); err != nil {
				return nil, err
			}
		}
		if state.FileID == "" || state.Name == "" {
			return nil, errors.New("139 returned incomplete file metadata")
		}
		return &model.Object{ID: state.FileID, Name: state.Name, Size: req.FileSize, Modified: time.Now()}, nil
	}
	var resp struct {
		XMLName    xml.Name `xml:"result"`
		ResultCode *int     `xml:"resultCode"`
		Msg        string   `xml:"msg"`
	}
	if err := xml.Unmarshal([]byte(result.Body), &resp); err != nil {
		return nil, err
	}
	if resp.ResultCode == nil || *resp.ResultCode != 0 {
		return nil, fmt.Errorf("139 rejected upload: %s", resp.Msg)
	}
	if state, ok := plan.State.(proxyUploadState); ok {
		return &model.Object{ID: state.FileID, Name: state.Name, Size: req.FileSize, Modified: time.Now()}, nil
	}
	return nil, nil
}

var _ driver.UploadProxyUploader = (*Yun139)(nil)
