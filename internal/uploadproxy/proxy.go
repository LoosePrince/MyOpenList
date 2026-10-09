package uploadproxy

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/pkg/sign"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/google/uuid"
)

type Rules struct {
	Hash      string `json:"hash"`
	MaxSize   int64  `json:"max_size"`
	BufferMax int64  `json:"buffer_max"`
}

type prepared struct {
	sync.Mutex
	request  model.UploadProxyRequest
	storage  driver.Driver
	dir      model.Obj
	actual   string
	plan     *model.UploadProxyPlan
	existing model.Obj
	expire   time.Time
	done     atomic.Bool
	result   model.UploadProxyResult
	err      error
}

var sessions = struct {
	sync.Mutex
	items map[string]*prepared
}{items: make(map[string]*prepared)}

func Enabled(storage driver.Driver) bool {
	u, ok := storage.(driver.UploadProxyUploader)
	return ok && !storage.Config().NoUpload && setting.GetBool(conf.UploadProxyEnabled) && u.GetUploadProxyConfig().UploadProxyURL != ""
}

func lifetime() time.Duration {
	return time.Duration(min(max(setting.GetInt(conf.UploadProxyExpiration, 900), 1), 86400)) * time.Second
}

func rules(storage driver.Driver) Rules {
	return Rules{
		Hash:      storage.(driver.UploadProxyUploader).UploadProxyHash(),
		MaxSize:   int64(min(max(setting.GetInt(conf.UploadProxyMaxSize, 100), 1), 1024)) << 20,
		BufferMax: int64(min(max(setting.GetInt(conf.UploadProxyBufferSize, 32), 1), 32)) << 20,
	}
}

func Validate(req model.UploadProxyRequest) error {
	if !strings.HasPrefix(req.Path, "/") || path.Clean(req.Path) != req.Path || strings.ContainsAny(req.Path, "\\\x00\r\n") {
		return errors.New("invalid upload directory")
	}
	if req.FileName == "" || req.FileName == "." || req.FileName == ".." || strings.ContainsAny(req.FileName, "/\\\x00\r\n") {
		return errors.New("invalid file name")
	}
	if req.FileSize < 0 || req.Method != http.MethodPut || (req.Format != "raw" && req.Format != "form") {
		return errors.New("upload requires PUT, a known size and raw or form format")
	}
	if req.Response != "json" && req.Response != "s3" && req.Response != "webdav" {
		return errors.New("invalid upload response format")
	}
	if req.ContentType == "" || strings.ContainsAny(req.ContentType, "\r\n") {
		return errors.New("invalid content type")
	}
	return nil
}

func signedData(req model.UploadProxyRequest) string {
	req.Sign, req.Hash = "", ""
	data, _ := json.Marshal(req)
	return "upload-proxy-v1:" + string(data)
}

func signer() sign.Sign { return sign.NewHMACSign([]byte(setting.GetStr(conf.Token))) }

func TokenValid(token string) bool {
	expected := setting.GetStr(conf.Token)
	return expected != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func address(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid upload proxy URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func resolve(req model.UploadProxyRequest) (driver.Driver, string, error) {
	if err := Validate(req); err != nil {
		return nil, "", err
	}
	storage, actual, err := op.GetStorageAndActualPath(req.Path)
	if err != nil {
		return nil, "", err
	}
	if !Enabled(storage) || storage.GetStorage().Disabled {
		return nil, "", errors.New("upload proxy is not enabled for this storage")
	}
	if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
		return nil, "", errs.StorageNotInit
	}
	target, _, err := op.GetStorageAndActualPath(path.Join(req.Path, req.FileName))
	if err != nil || target != storage {
		return nil, "", errs.PermissionDenied
	}
	r := rules(storage)
	if req.FileSize > r.MaxSize || ((r.Hash != "" || req.Format == "form") && req.FileSize > r.BufferMax) {
		return nil, "", errors.New("file exceeds upload proxy size limit")
	}
	if setting.GetBool(conf.IgnoreSystemFiles) && utils.IsSystemFile(req.FileName) {
		return nil, "", errs.IgnoredSystemFile
	}
	return storage, actual, nil
}

// Issue only creates a client capability. No file content or provider credentials are returned.
func Issue(req model.UploadProxyRequest) (*model.HttpDirectUploadInfo, error) {
	storage, _, err := resolve(req)
	if err != nil {
		return nil, err
	}
	u, err := address(storage.(driver.UploadProxyUploader).GetUploadProxyConfig().UploadProxyURL)
	if err != nil {
		return nil, err
	}
	req.WorkerAddress = u
	req.StorageID, req.StorageTime = storage.GetStorage().ID, storage.GetStorage().Modified.UnixMilli()
	req.Sign, req.Hash = "", ""
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	query := url.Values{"payload": {base64.RawURLEncoding.EncodeToString(data)}}
	if !storage.(driver.UploadProxyUploader).GetUploadProxyConfig().DisableUploadProxySign {
		if setting.GetStr(conf.Token) == "" {
			return nil, errors.New("OpenList Token is empty")
		}
		query.Set("sign", signer().Sign(signedData(req), time.Now().Add(lifetime()).Unix()))
	}
	return &model.HttpDirectUploadInfo{UploadURL: u + "/upload?" + query.Encode(), Method: http.MethodPut}, nil
}

func Verify(req model.UploadProxyRequest, token string, disableSign bool) (Rules, error) {
	storage, _, err := resolve(req)
	if err != nil {
		return Rules{}, err
	}
	u := storage.(driver.UploadProxyUploader)
	configured, err := address(u.GetUploadProxyConfig().UploadProxyURL)
	if err != nil || configured != req.WorkerAddress {
		return Rules{}, errors.New("upload proxy address mismatch")
	}
	if disableSign {
		if !u.GetUploadProxyConfig().DisableUploadProxySign {
			return Rules{}, errors.New("anonymous upload is disabled for this storage")
		}
	} else {
		if !TokenValid(token) {
			return Rules{}, errors.New("invalid OpenList Token")
		}
		if req.StorageID != storage.GetStorage().ID || req.StorageTime != storage.GetStorage().Modified.UnixMilli() {
			return Rules{}, errors.New("storage configuration changed")
		}
		exp, err := strconv.ParseInt(req.Sign[strings.LastIndex(req.Sign, ":")+1:], 10, 64)
		if err != nil || exp <= 0 {
			return Rules{}, errors.New("invalid signature expiration")
		}
		if err := signer().Verify(signedData(req), req.Sign); err != nil {
			return Rules{}, err
		}
	}
	return rules(storage), nil
}

func Prepare(ctx context.Context, req model.UploadProxyRequest, token string, disableSign bool) (string, *model.UploadProxyPlan, error) {
	r, err := Verify(req, token, disableSign)
	if err != nil {
		return "", nil, err
	}
	if r.Hash != "" {
		width := 16
		if r.Hash == "sha256" {
			width = 32
		}
		decoded, err := hex.DecodeString(req.Hash)
		if err != nil || len(decoded) != width {
			return "", nil, errors.New("invalid file hash")
		}
	}
	storage, actual, err := resolve(req)
	if err != nil {
		return "", nil, err
	}
	req.StorageID, req.StorageTime = storage.GetStorage().ID, storage.GetStorage().Modified.UnixMilli()
	id := uuid.NewString()
	s := &prepared{request: req, storage: storage, actual: actual, expire: time.Now().Add(lifetime())}
	s.Lock()
	defer s.Unlock()
	sessions.Lock()
	for key, item := range sessions.items {
		if time.Now().After(item.expire) {
			delete(sessions.items, key)
		} else if !item.done.Load() && item.storage == storage && path.Join(item.request.Path, item.request.FileName) == path.Join(req.Path, req.FileName) {
			sessions.Unlock()
			return "", nil, errors.New("an upload to this destination is already in progress")
		}
	}
	if len(sessions.items) >= 1024 {
		sessions.Unlock()
		return "", nil, errors.New("too many upload proxy sessions")
	}
	sessions.items[id] = s
	sessions.Unlock()
	remove := func() { sessions.Lock(); delete(sessions.items, id); sessions.Unlock() }
	existing, err := op.GetUnwrap(ctx, storage, path.Join(actual, req.FileName))
	if err == nil {
		if existing.IsDir() || !req.Overwrite {
			remove()
			return "", nil, errs.ObjectAlreadyExists
		}
	} else if !errs.IsObjectNotFound(err) {
		remove()
		return "", nil, err
	}
	if err = op.MakeDir(ctx, storage, actual); err != nil {
		remove()
		return "", nil, err
	}
	s.dir, err = op.GetUnwrap(ctx, storage, actual)
	if err != nil {
		remove()
		return "", nil, err
	}
	if model.ObjHasMask(s.dir, model.NoWrite) {
		remove()
		return "", nil, errs.PermissionDenied
	}
	s.existing = existing
	s.plan, err = storage.(driver.UploadProxyUploader).PrepareUploadProxy(ctx, s.dir, req)
	if err != nil {
		remove()
		return "", nil, err
	}
	if s.plan == nil {
		remove()
		return "", nil, errors.New("empty upload plan")
	}
	if !s.plan.Instant {
		if _, err = address(s.plan.URL); err != nil {
			// Provider upload URLs may carry signed query parameters.
			u, parseErr := url.Parse(s.plan.URL)
			if parseErr != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
				remove()
				return "", nil, errors.New("invalid provider upload URL")
			}
		}
	}
	return id, s.plan, nil
}

func Complete(ctx context.Context, id, token string, disableSign bool, result model.UploadProxyResult) error {
	sessions.Lock()
	s := sessions.items[id]
	sessions.Unlock()
	if s == nil {
		return errors.New("upload session not found")
	}
	s.Lock()
	defer s.Unlock()
	if time.Now().After(s.expire) {
		return errors.New("upload session expired")
	}
	storage, _, err := resolve(s.request)
	if err != nil || storage != s.storage || storage.GetStorage().ID != s.request.StorageID || storage.GetStorage().Modified.UnixMilli() != s.request.StorageTime {
		return errors.New("upload storage changed or disabled")
	}
	if disableSign {
		if !storage.(driver.UploadProxyUploader).GetUploadProxyConfig().DisableUploadProxySign {
			return errors.New("anonymous upload is disabled")
		}
	} else if !TokenValid(token) {
		return errors.New("invalid OpenList Token")
	}
	if s.done.Load() {
		if s.result != result {
			return errors.New("conflicting completion notification")
		}
		return s.err
	}
	if s.plan == nil {
		return errors.New("upload not prepared")
	}
	if len(result.Body) > 64<<10 || (result.Success && (result.Bytes != s.request.FileSize || result.Status < 200 || result.Status >= 300)) {
		return errors.New("invalid upload completion result")
	}
	defer s.done.Store(true)
	s.result = result
	if !result.Success {
		s.err = errors.New("worker upload failed")
		return s.err
	}
	obj, err := storage.(driver.UploadProxyUploader).CompleteUploadProxy(ctx, s.dir, s.request, s.plan, result)
	if err == nil && obj != nil && s.existing != nil && s.existing.GetID() != obj.GetID() {
		if !s.request.Overwrite {
			err = errs.ObjectAlreadyExists
		} else if remover, ok := storage.(driver.Remove); ok {
			err = remover.Remove(ctx, s.existing)
		} else {
			err = errs.NotImplement
		}
	}
	if err == nil && obj != nil && obj.GetName() != s.request.FileName {
		if s.existing == nil {
			existing, getErr := op.GetUnwrap(ctx, storage, path.Join(s.actual, s.request.FileName))
			if getErr == nil && existing.GetID() != obj.GetID() {
				if !s.request.Overwrite {
					err = errs.ObjectAlreadyExists
				} else if remover, ok := storage.(driver.Remove); ok {
					err = remover.Remove(ctx, existing)
				} else {
					err = errs.NotImplement
				}
			} else if getErr != nil && !errs.IsObjectNotFound(getErr) {
				err = getErr
			}
		}
		if err == nil {
			if renamer, ok := storage.(driver.Rename); ok {
				err = renamer.Rename(ctx, obj, s.request.FileName)
			} else {
				err = errs.NotImplement
			}
		}
	}
	if err == nil {
		op.UploadProxyCompleted(ctx, storage, s.actual, s.request.FileName)
	}
	s.err = err
	return err
}

func Request(fullPath string, size int64, contentType, format, response string, overwrite bool) model.UploadProxyRequest {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return model.UploadProxyRequest{Path: path.Dir(fullPath), FileName: path.Base(fullPath), FileSize: size, Method: "PUT", ContentType: contentType, Format: format, Response: response, Overwrite: overwrite}
}

func RejectMultipart(fullPath string) error {
	storage, _, err := op.GetStorageAndActualPath(path.Dir(fullPath))
	if err == nil && Enabled(storage) {
		return fmt.Errorf("upload proxy does not support multipart uploads")
	}
	return nil
}
