package handles

import (
	"net/http"
	"net/url"
	"path"
	"strconv"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/uploadproxy"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

type proxyControlRequest struct {
	model.UploadProxyRequest
	DisableSign bool `json:"disable_sign"`
}

func UploadProxyVerify(c *gin.Context) {
	var req proxyControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	rules, err := uploadproxy.Verify(req.UploadProxyRequest, c.GetHeader("Authorization"), req.DisableSign)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	common.SuccessResp(c, rules)
}

func UploadProxyPrepare(c *gin.Context) {
	var req proxyControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	id, plan, err := uploadproxy.Prepare(c.Request.Context(), req.UploadProxyRequest, c.GetHeader("Authorization"), req.DisableSign)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	common.SuccessResp(c, gin.H{"upload_id": id, "plan": plan})
}

func UploadProxyComplete(c *gin.Context) {
	var req struct {
		UploadID    string                  `json:"upload_id"`
		DisableSign bool                    `json:"disable_sign"`
		Result      model.UploadProxyResult `json:"result"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if err := uploadproxy.Complete(c.Request.Context(), req.UploadID, c.GetHeader("Authorization"), req.DisableSign, req.Result); err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	common.SuccessResp(c)
}

// Runs before either upload handler reads or drains the request body.
func redirectUploadProxy(c *gin.Context, fullPath, format string) bool {
	storage, _, err := op.GetStorageAndActualPath(path.Dir(fullPath))
	if err != nil || !uploadproxy.Enabled(storage) {
		return false
	}
	size := c.Request.ContentLength
	if format == "form" || size < 0 {
		size, err = strconv.ParseInt(c.GetHeader("X-File-Size"), 10, 64)
		if err != nil {
			common.ErrorStrResp(c, "upload proxy requires X-File-Size for forms or unknown Content-Length", 400)
			return true
		}
	}
	contentType := c.GetHeader("Content-Type")
	if format == "form" {
		contentType = "application/octet-stream"
	}
	info, err := uploadproxy.Issue(uploadproxy.Request(fullPath, size, contentType, format, "json", c.GetHeader("Overwrite") != "false"))
	if err != nil {
		common.ErrorResp(c, err, 400)
		return true
	}
	writeProxyRedirect(c, info.UploadURL)
	return true
}

func writeProxyRedirect(c *gin.Context, location string) {
	c.Header("Location", location)
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.JSON(http.StatusTemporaryRedirect, common.Resp[any]{Code: 307, Message: "upload via proxy", Data: gin.H{"upload_url": location, "method": "PUT", "chunk_size": 0}})
	c.Abort()
}

func redirectUploadProxyPath(c *gin.Context, format string) bool {
	decoded, err := url.PathUnescape(c.GetHeader("File-Path"))
	if err != nil {
		common.ErrorResp(c, err, 400)
		return true
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	fullPath, err := user.JoinPath(decoded)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return true
	}
	return redirectUploadProxy(c, fullPath, format)
}
