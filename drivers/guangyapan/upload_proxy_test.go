package guangyapan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func TestGuangYaProxyAuthorizationAndCompletion(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("missing provider API authorization")
		}
		switch r.URL.Path {
		case "/nd.bizuserres.s/v1/get_res_center_token":
			var body struct {
				Name string         `json:"name"`
				Res  map[string]any `json:"res"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, present := body.Res["md5"]; present || body.Name != "file.txt" || body.Res["fileSize"] != float64(3) {
				t.Errorf("unexpected streaming upload parameters: %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"taskId":"task","objectPath":"files/file.txt","bucketName":"bucket","endPoint":"https://oss-cn-hangzhou.aliyuncs.com","accessKeyID":"access","secretAccessKey":"private-secret","sessionToken":"temporary-token"}}`))
		case "/nd.bizuserres.s/v1/file/get_info_by_task_id":
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"fileId":"uploaded-id"}}`))
		case "/userres/v1/file/get_file_list":
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"list":[{"fileId":"uploaded-id","fileName":"file(1).txt","fileSize":3}]}}`))
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	d := &GuangYaPan{Addition: Addition{AccessToken: "test-access", PageSize: 100}, apiClient: resty.New().SetBaseURL(server.URL)}
	dir := &model.Object{ID: "parent"}
	req := model.UploadProxyRequest{FileName: "file.txt", FileSize: 3, ContentType: "text/plain"}
	plan, err := d.PrepareUploadProxy(context.Background(), dir, req)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(plan.URL)
	if err != nil || u.Host != "bucket.oss-cn-hangzhou.aliyuncs.com" || u.Query().Get("Signature") == "" || plan.Method != "PUT" {
		t.Fatalf("missing single PUT authorization: %+v, %v", plan, err)
	}
	wire, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(wire), "private-secret") || strings.Contains(string(wire), `"task"`) {
		t.Fatalf("private signing key or completion state serialized: %s, %v", wire, err)
	}
	obj, err := d.CompleteUploadProxy(context.Background(), dir, req, plan, model.UploadProxyResult{})
	if err != nil || obj == nil || obj.GetID() != "uploaded-id" || obj.GetName() != "file(1).txt" {
		t.Fatalf("completion lost provider result: %v, %v", obj, err)
	}
	if len(calls) != 4 {
		t.Fatalf("unexpected provider control requests: %v", calls)
	}
}

func TestGuangYaProxyRejectsFailedTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":999,"msg":"upload failed"}`))
	}))
	defer server.Close()
	d := &GuangYaPan{Addition: Addition{AccessToken: "test-access"}, apiClient: resty.New().SetBaseURL(server.URL)}
	_, err := d.CompleteUploadProxy(context.Background(), nil, model.UploadProxyRequest{}, &model.UploadProxyPlan{State: "task"}, model.UploadProxyResult{})
	if err == nil {
		t.Fatal("failed provider task reported success")
	}
}
