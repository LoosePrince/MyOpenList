package _139

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func Test139ProxySinglePartAndCompletion(t *testing.T) {
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/file/create" {
			parts := body["partInfos"].([]any)
			if len(parts) != 1 || parts[0].(map[string]any)["partSize"] != float64(3) || body["contentHashAlgorithm"] != "SHA256" {
				t.Errorf("unexpected upload: %+v", body)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"fileId":"7","fileName":"file.txt","uploadId":"up","partInfos":[{"partNumber":1,"uploadUrl":"https://provider.test/upload"}]}}`))
		} else if r.URL.Path == "/file/complete" {
			completed = body["fileId"] == "7" && body["uploadId"] == "up"
			_, _ = w.Write([]byte(`{"success":true}`))
		} else {
			t.Errorf("unexpected URL: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	old := base.RestyClient
	base.RestyClient = resty.New()
	t.Cleanup(func() { base.RestyClient = old })
	d := &Yun139{Addition: Addition{Type: MetaPersonalNew}, PersonalCloudHost: server.URL}
	req := model.UploadProxyRequest{FileName: "file.txt", FileSize: 3, Hash: "hash", ContentType: "text/plain"}
	plan, err := d.PrepareUploadProxy(context.Background(), &model.Object{ID: "42"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.URL != "https://provider.test/upload" || plan.Method != "PUT" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	obj, err := d.CompleteUploadProxy(context.Background(), nil, req, plan, model.UploadProxyResult{})
	if err != nil || obj.GetID() != "7" || !completed {
		t.Fatalf("result: %v, %v, completed=%v", obj, err, completed)
	}
}

func Test139ProxyLegacyBusinessResult(t *testing.T) {
	d := &Yun139{}
	for _, body := range []string{`<result><resultCode>1</resultCode><msg>failed</msg></result>`, `<result><msg>failed</msg></result>`, `not xml`} {
		if _, err := d.CompleteUploadProxy(context.Background(), nil, model.UploadProxyRequest{}, &model.UploadProxyPlan{}, model.UploadProxyResult{Body: body}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := d.CompleteUploadProxy(context.Background(), nil, model.UploadProxyRequest{}, &model.UploadProxyPlan{}, model.UploadProxyResult{Body: `<result><resultCode>0</resultCode></result>`}); err != nil {
		t.Fatal(err)
	}
	plan := &model.UploadProxyPlan{State: proxyUploadState{FileID: "7", Name: "file(1).txt"}}
	obj, err := d.CompleteUploadProxy(context.Background(), nil, model.UploadProxyRequest{FileSize: 3}, plan, model.UploadProxyResult{Body: `<result><resultCode>0</resultCode></result>`})
	if err != nil || obj == nil || obj.GetID() != "7" || obj.GetName() != "file(1).txt" {
		t.Fatalf("legacy completion lost provider metadata: %v, %v", obj, err)
	}
}

type proxyControlTransport func(*http.Request) (*http.Response, error)

func (f proxyControlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func Test139ProxyLegacySingleRequest(t *testing.T) {
	calls := 0
	old := base.RestyClient
	base.RestyClient = resty.New().SetTransport(proxyControlTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/orchestration/personalCloud/uploadAndDownload/v1.0/pcUploadFileRequest" {
			t.Errorf("unexpected control endpoint: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"result":{"resultCode":"0"},"uploadResult":{"uploadtaskID":"task","redirectionUrl":"https://provider.test/upload","newContentIDList":[{"contentID":"7","contentName":"file(1).txt"}]}}}`)), Request: r}, nil
	}))
	t.Cleanup(func() { base.RestyClient = old })
	d := &Yun139{Addition: Addition{Type: MetaPersonal, ReportRealSize: true}}
	req := model.UploadProxyRequest{FileName: "file.txt", FileSize: 3}
	plan, err := d.PrepareUploadProxy(context.Background(), &model.Object{ID: "42"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != "POST" || plan.Headers["range"] != "bytes=0-2" || plan.Headers["contentSize"] != "3" || plan.Headers["uploadtaskID"] != "task" {
		t.Fatalf("unexpected legacy upload plan: %+v", plan)
	}
	obj, err := d.CompleteUploadProxy(context.Background(), nil, req, plan, model.UploadProxyResult{Body: `<result><resultCode>0</resultCode></result>`})
	if err != nil || obj == nil || obj.GetID() != "7" || obj.GetName() != "file(1).txt" {
		t.Fatalf("completion: %v, %v", obj, err)
	}
	req.FileSize = 0
	if _, err = d.PrepareUploadProxy(context.Background(), nil, req); err == nil {
		t.Fatal("empty file accepted by legacy upload")
	}
	if calls != 1 {
		t.Fatalf("unexpected provider control request count: %d", calls)
	}
}
