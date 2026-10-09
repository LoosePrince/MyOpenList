package handles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/local"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type redirectProxyAddition struct {
	local.Addition
	driver.UploadProxy
}

type redirectProxyDriver struct {
	local.Local
	addition redirectProxyAddition
}

func (d *redirectProxyDriver) Config() driver.Config          { return driver.Config{Name: "RedirectProxyTest"} }
func (d *redirectProxyDriver) GetAddition() driver.Additional { return &d.addition }
func (d *redirectProxyDriver) Init(ctx context.Context) error {
	d.Local.Addition = d.addition.Addition
	return d.Local.Init(ctx)
}
func (d *redirectProxyDriver) GetUploadProxyConfig() *driver.UploadProxy {
	return &d.addition.UploadProxy
}
func (d *redirectProxyDriver) UploadProxyHash() string { return "" }
func (d *redirectProxyDriver) PrepareUploadProxy(context.Context, model.Obj, model.UploadProxyRequest) (*model.UploadProxyPlan, error) {
	return nil, errs.NotImplement
}
func (d *redirectProxyDriver) CompleteUploadProxy(context.Context, model.Obj, model.UploadProxyRequest, *model.UploadProxyPlan, model.UploadProxyResult) (model.Obj, error) {
	return nil, errs.NotImplement
}

type unreadProxyBody struct{ reads int }

func (b *unreadProxyBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (*unreadProxyBody) Close() error               { return nil }

func TestUploadProxyRedirectDoesNotReadFile(t *testing.T) {
	previousConf := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	t.Cleanup(func() { conf.Conf = previousConf })
	previousCache := op.Cache
	op.Cache = op.NewCacheManager()
	t.Cleanup(func() { op.Cache = previousCache })
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	db.Init(database)
	for key, value := range map[string]string{conf.Token: "test-token", conf.UploadProxyEnabled: "true"} {
		op.Cache.SetSetting(key, &model.SettingItem{Key: key, Value: value})
	}
	op.RegisterDriver(func() driver.Driver { return &redirectProxyDriver{} })
	addition, _ := json.Marshal(map[string]any{"root_folder_path": t.TempDir(), "upload_proxy_url": "https://worker.test"})
	mount := "/" + t.Name()
	created, err := op.CreateStorage(context.Background(), model.Storage{Driver: "RedirectProxyTest", MountPath: mount, Addition: string(addition)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.DeleteStorageById(context.Background(), created) })
	for _, format := range []string{"raw", "form"} {
		t.Run(format, func(t *testing.T) {
			body := &unreadProxyBody{}
			r := httptest.NewRequest("PUT", "/api/fs/put", nil)
			r.Body, r.ContentLength = body, 3
			r.Header.Set("File-Path", mount+"/file.txt")
			r.Header.Set("X-File-Size", "3")
			r = r.WithContext(context.WithValue(r.Context(), conf.UserKey, &model.User{BasePath: "/", Role: model.ADMIN}))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = r
			if format == "raw" {
				FsStream(c)
			} else {
				FsForm(c)
			}
			if w.Code != http.StatusTemporaryRedirect || !strings.HasPrefix(w.Header().Get("Location"), "https://worker.test/upload?") || body.reads != 0 {
				t.Fatalf("response=%d, location=%s, body reads=%d, body=%s", w.Code, w.Header().Get("Location"), body.reads, w.Body.String())
			}
		})
	}
	for _, item := range op.GetDriverInfoMap()["Local"].Additional {
		if item.Name == "upload_proxy_url" {
			t.Fatal("unsupported driver advertises upload proxy configuration")
		}
	}
}
