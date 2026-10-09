package s3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/local"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type s3ProxyAddition struct {
	local.Addition
	driver.UploadProxy
}

type s3ProxyDriver struct {
	local.Local
	addition s3ProxyAddition
}

func (d *s3ProxyDriver) Config() driver.Config          { return driver.Config{Name: "S3ProxyTest"} }
func (d *s3ProxyDriver) GetAddition() driver.Additional { return &d.addition }
func (d *s3ProxyDriver) Init(ctx context.Context) error {
	d.Local.Addition = d.addition.Addition
	return d.Local.Init(ctx)
}
func (d *s3ProxyDriver) GetUploadProxyConfig() *driver.UploadProxy { return &d.addition.UploadProxy }
func (d *s3ProxyDriver) UploadProxyHash() string                   { return "" }
func (d *s3ProxyDriver) PrepareUploadProxy(context.Context, model.Obj, model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	return nil, errs.NotImplement
}
func (d *s3ProxyDriver) CompleteUploadProxy(context.Context, model.Obj, model.UploadProxyRequest, *model.UploadProxyPlan, model.UploadProxyResult) (model.Obj, error) {
	return nil, errs.NotImplement
}

type s3UnreadBody struct{ reads int }

func (b *s3UnreadBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (*s3UnreadBody) Close() error               { return nil }

func TestS3ProxyRedirectDoesNotReadFile(t *testing.T) {
	previousCache := op.Cache
	op.Cache = op.NewCacheManager()
	t.Cleanup(func() { op.Cache = previousCache })
	for key, value := range map[string]string{conf.Token: "test-token", conf.UploadProxyEnabled: "true", conf.S3Buckets: `[{"name":"proxy","path":"/s3proxytest"}]`} {
		op.Cache.SetSetting(key, &model.SettingItem{Key: key, Value: value})
	}
	op.RegisterDriver(func() driver.Driver { return &s3ProxyDriver{} })
	addition, _ := json.Marshal(map[string]any{"root_folder_path": t.TempDir(), "upload_proxy_url": "https://worker.test"})
	id, err := op.CreateStorage(context.Background(), model.Storage{Driver: "S3ProxyTest", MountPath: "/s3proxytest", Addition: string(addition)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.DeleteStorageById(context.Background(), id) })
	for _, query := range []string{"", "?uploads", "?uploadId=id&partNumber=1"} {
		t.Run(query, func(t *testing.T) {
			body := &s3UnreadBody{}
			r := httptest.NewRequest("PUT", "/proxy/file.txt"+query, nil)
			r.Body, r.ContentLength = body, 3
			r.Header.Set("Content-Type", "text/plain")
			r.Header.Set("Content-MD5", "kAFQmDzST7DWlj99KOF/cg==")
			w := httptest.NewRecorder()
			redirectHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("proxy request reached the file upload handler") }), nil).ServeHTTP(w, r)
			want := http.StatusTemporaryRedirect
			if query != "" {
				want = http.StatusNotImplemented
			}
			if w.Code != want || body.reads != 0 {
				t.Fatalf("status=%d, body reads=%d, response=%s", w.Code, body.reads, w.Body.String())
			}
			if query != "" {
				return
			}
			u, err := url.Parse(w.Header().Get("Location"))
			if err != nil || u.Host != "worker.test" {
				t.Fatalf("location: %v, %v", u, err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(u.Query().Get("payload"))
			var desc model.UploadProxyRequest
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(payload, &desc); err != nil {
				t.Fatal(err)
			}
			if desc.Path != "/s3proxytest" || desc.Response != "s3" || desc.ContentMD5 != "kAFQmDzST7DWlj99KOF/cg==" {
				t.Fatalf("unexpected descriptor: %+v", desc)
			}
		})
	}
}
