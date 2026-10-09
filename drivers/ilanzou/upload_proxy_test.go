package ilanzou

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func TestILanzouSingleRequestProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proved/7n/getUpToken":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["md5"] != "900150983cd24fb0d6963f7d28e17f72" || body["folderId"] != "42" || body["fileSize"] != float64(1) {
				t.Errorf("unexpected initialization: %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":200,"upToken":"qiniu-token"}`))
		case "/unproved/7n/results":
			if r.URL.Query().Get("tokenList") != "result-token" {
				t.Error("wrong result token")
			}
			_, _ = w.Write([]byte(`{"code":200,"list":[{"fileId":7,"fileName":"file.txt","status":1}]}`))
		default:
			t.Errorf("unexpected control request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	d := &ILanZou{conf: Conf{base: server.URL, proved: "proved", unproved: "unproved", secret: []byte("lanZouY-disk-app")}, apiClient: resty.New(), account: "test"}
	req := model.UploadProxyRequest{FileName: "file.txt", FileSize: 3, Hash: "900150983cd24fb0d6963f7d28e17f72"}
	plan, err := d.PrepareUploadProxy(context.Background(), &model.Object{ID: "42"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Encoding != "multipart" || plan.Fields["token"] != "qiniu-token" || plan.URL != "https://upload.qiniup.com/" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	obj, err := d.CompleteUploadProxy(context.Background(), nil, req, plan, model.UploadProxyResult{Body: `{"token":"result-token"}`})
	if err != nil || obj.GetID() != "7" {
		t.Fatalf("result: %v, %v", obj, err)
	}
}

func TestILanzouProxyRejectsMissingFileMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/proved/7n/getUpToken" {
			_, _ = w.Write([]byte(`{"code":200,"upToken":"-1","map":{}}`))
		} else {
			_, _ = w.Write([]byte(`{"code":200,"list":[{"status":1}]}`))
		}
	}))
	defer server.Close()
	d := &ILanZou{conf: Conf{base: server.URL, proved: "proved", unproved: "unproved", secret: []byte("lanZouY-disk-app")}, apiClient: resty.New()}
	if _, err := d.PrepareUploadProxy(context.Background(), &model.Object{ID: "42"}, model.UploadProxyRequest{}); err == nil {
		t.Fatal("missing instant upload metadata accepted")
	}
	if _, err := d.CompleteUploadProxy(context.Background(), nil, model.UploadProxyRequest{}, &model.UploadProxyPlan{}, model.UploadProxyResult{Body: `{"token":"result"}`}); err == nil {
		t.Fatal("missing completed file metadata accepted")
	}
}
