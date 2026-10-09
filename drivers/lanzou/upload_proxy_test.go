package lanzou

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestLanzouProxyPlanAndBusinessResult(t *testing.T) {
	d := &LanZou{Addition: Addition{Type: "cookie", Cookie: "session=test", BaseUrl: "https://pc.woozooo.com", UserAgent: "test"}}
	req := model.UploadProxyRequest{FileName: "file.txt", FileSize: 3}
	plan, err := d.PrepareUploadProxy(context.Background(), &model.Object{ID: "42"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != "POST" || plan.Encoding != "multipart" || plan.FileField != "upload_file" || plan.Fields["folder_id_bb_n"] != "42" || plan.Headers["Cookie"] != "session=test" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	for _, body := range []string{`{"zt":0,"text":[]}`, `{"zt":4,"text":[]}`, `{"zt":1,"text":[]}`, `<html>challenge</html>`} {
		if _, err := d.CompleteUploadProxy(context.Background(), nil, req, plan, model.UploadProxyResult{Body: body}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	obj, err := d.CompleteUploadProxy(context.Background(), nil, req, plan, model.UploadProxyResult{Body: `{"zt":1,"text":[{"id":"7","name_all":"file.txt","size":"3 B"}]}`})
	if err != nil || obj.GetID() != "7" {
		t.Fatalf("result: %v, %v", obj, err)
	}
	d.Type = "url"
	if _, err := d.PrepareUploadProxy(context.Background(), nil, req); err == nil {
		t.Fatal("share mount accepted upload")
	}
}
