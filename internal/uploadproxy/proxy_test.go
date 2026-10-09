package uploadproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testProxyDriver struct {
	model.Storage
	driver.UploadProxy
	prepared  int
	completed int
}

func (d *testProxyDriver) Config() driver.Config          { return driver.Config{Name: "UploadProxyTest"} }
func (d *testProxyDriver) GetAddition() driver.Additional { return &d.UploadProxy }
func (d *testProxyDriver) Init(context.Context) error     { return nil }
func (d *testProxyDriver) Drop(context.Context) error     { return nil }
func (d *testProxyDriver) GetRoot(context.Context) (model.Obj, error) {
	return &model.Object{Name: "root", IsFolder: true}, nil
}
func (d *testProxyDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *testProxyDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}
func (d *testProxyDriver) Get(_ context.Context, actual string) (model.Obj, error) {
	if actual == "/" {
		return &model.Object{Name: "root", IsFolder: true}, nil
	}
	return nil, errs.ObjectNotFound
}
func (d *testProxyDriver) UploadProxyHash() string { return "" }
func (d *testProxyDriver) PrepareUploadProxy(context.Context, model.Obj, model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	d.prepared++
	return &model.UploadProxyPlan{URL: "https://provider.test/upload", Method: "PUT"}, nil
}
func (d *testProxyDriver) CompleteUploadProxy(context.Context, model.Obj, model.UploadProxyRequest, *model.UploadProxyPlan, model.UploadProxyResult) (model.Obj, error) {
	d.completed++
	return nil, nil
}

func setupProxy(t *testing.T) *testProxyDriver {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conf.Conf = conf.DefaultConfig(t.TempDir())
	db.Init(database)
	op.Cache = op.NewCacheManager()
	for key, value := range map[string]string{conf.Token: "test-openlist-token", conf.UploadProxyEnabled: "true", conf.UploadProxyMaxSize: "100", conf.UploadProxyBufferSize: "32", conf.UploadProxyExpiration: "900", conf.IgnoreSystemFiles: "false"} {
		op.Cache.SetSetting(key, &model.SettingItem{Key: key, Value: value})
	}
	mount := "/" + t.Name()
	op.RegisterDriver(func() driver.Driver { return &testProxyDriver{} })
	if _, err = op.CreateStorage(context.Background(), model.Storage{Driver: "UploadProxyTest", MountPath: mount, Addition: `{"upload_proxy_url":"https://worker.test"}`}); err != nil {
		t.Fatal(err)
	}
	storage, err := op.GetStorageByMountPath(mount)
	if err != nil {
		t.Fatal(err)
	}
	return storage.(*testProxyDriver)
}

func issued(t *testing.T, storage *testProxyDriver) model.UploadProxyRequest {
	t.Helper()
	info, err := Issue(Request(storage.MountPath+"/file.txt", 3, "text/plain", "raw", "json", false))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(info.UploadURL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(u.Query().Get("payload"))
	if err != nil {
		t.Fatal(err)
	}
	var req model.UploadProxyRequest
	if err = json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	req.Sign = u.Query().Get("sign")
	if strings.Contains(info.UploadURL, "test-openlist-token") {
		t.Fatal("Token exposed to client")
	}
	return req
}

func TestProxySignatureBindsUploadRules(t *testing.T) {
	d := setupProxy(t)
	req := issued(t, d)
	if _, err := Verify(req, "test-openlist-token", false); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*model.UploadProxyRequest){
		"path":         func(r *model.UploadProxyRequest) { r.Path += "/sub" },
		"filename":     func(r *model.UploadProxyRequest) { r.FileName = "other.txt" },
		"size":         func(r *model.UploadProxyRequest) { r.FileSize++ },
		"method":       func(r *model.UploadProxyRequest) { r.Method = "POST" },
		"format":       func(r *model.UploadProxyRequest) { r.Format = "form" },
		"content type": func(r *model.UploadProxyRequest) { r.ContentType = "application/json" },
		"overwrite":    func(r *model.UploadProxyRequest) { r.Overwrite = true },
		"worker":       func(r *model.UploadProxyRequest) { r.WorkerAddress = "https://other.test" },
		"response":     func(r *model.UploadProxyRequest) { r.Response = "s3" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			copy := req
			change(&copy)
			if _, err := Verify(copy, "test-openlist-token", false); err == nil {
				t.Fatal("tampered descriptor accepted")
			}
		})
	}
	if _, err := Verify(req, "login-jwt", false); err == nil {
		t.Fatal("login token accepted as Worker Token")
	}
	req.Sign = signer().Sign(signedData(req), time.Now().Add(-time.Minute).Unix())
	if _, err := Verify(req, "test-openlist-token", false); err == nil {
		t.Fatal("expired signature accepted")
	}
}

func TestProxyAnonymousModeAndCompletion(t *testing.T) {
	d := setupProxy(t)
	req := issued(t, d)
	if _, err := Verify(req, "", true); err == nil {
		t.Fatal("anonymous mode unexpectedly enabled")
	}
	d.DisableUploadProxySign = true
	req = Request(d.MountPath+"/file.txt", 3, "text/plain", "raw", "json", false)
	req.WorkerAddress = "https://worker.test"
	id, plan, err := Prepare(context.Background(), req, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.URL == "" || d.prepared != 1 {
		t.Fatal("provider upload was not prepared")
	}
	result := model.UploadProxyResult{Success: true, Status: 200, Bytes: 2}
	if err = Complete(context.Background(), id, "", true, result); err == nil {
		t.Fatal("short upload accepted")
	}
	result.Bytes = 3
	if err = Complete(context.Background(), id, "", true, result); err != nil {
		t.Fatal(err)
	}
	if err = Complete(context.Background(), id, "", true, result); err != nil {
		t.Fatal(err)
	}
	if d.completed != 1 {
		t.Fatalf("completion called %d times", d.completed)
	}
	result.Status = 201
	if err = Complete(context.Background(), id, "", true, result); err == nil {
		t.Fatal("conflicting callback accepted")
	}
}

func TestProxyRejectsInvalidTargets(t *testing.T) {
	d := setupProxy(t)
	for _, name := range []string{"../file", `..\file`, ".", "", "sub/file"} {
		req := Request(d.MountPath+"/file.txt", 3, "text/plain", "raw", "json", true)
		req.FileName = name
		if _, err := Issue(req); err == nil {
			t.Fatalf("invalid name %q accepted", name)
		}
	}
	if _, err := Issue(Request(d.MountPath+"/file.txt", 101<<20, "text/plain", "raw", "json", true)); err == nil {
		t.Fatal("oversized file accepted")
	}
	if err := RejectMultipart(d.MountPath + "/file.txt"); err == nil {
		t.Fatal("multipart accepted")
	}
}
