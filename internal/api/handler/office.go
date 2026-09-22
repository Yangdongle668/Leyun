package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// OfficeConfig 下发在线编辑器配置。
func (h *Handler) OfficeConfig(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	nodeID := uintQuery(c, "node_id", 0)
	if spaceID == 0 || nodeID == 0 {
		response.Fail(c, response.BadRequest("请指定要编辑的文件"))
		return
	}
	cfg, err := h.svc.Office.BuildConfig(subj, spaceID, nodeID, requestBase(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, cfg)
}

// OfficeContent 供 Document Server 拉取文件内容。
//
// 这个接口不走登录态：Document Server 是独立服务，带的是乐云下发的一次性资源令牌。
func (h *Handler) OfficeContent(c *gin.Context) {
	node, err := h.svc.Office.ResolveContentToken(c.Query("token"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	f, openErr := h.svc.File.OpenContent(node)
	if openErr != nil {
		response.Fail(c, openErr)
		return
	}
	defer f.Close()

	c.Header("Content-Type", node.MimeType)
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, node.Name, node.UpdatedAt, f)
}

// OfficeCallback 接收 Document Server 的保存回调。
//
// 无论成功失败都必须回 {"error":0}/{"error":1} 这种固定结构，
// 否则 Document Server 会判定保存失败并不断重试。
func (h *Handler) OfficeCallback(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": 1})
		return
	}
	if err := h.svc.Office.HandleCallback(c.Query("token"), c.GetHeader("Authorization"), body); err != nil {
		logx.Error("Office 回调处理失败", "err", err)
		c.JSON(http.StatusOK, gin.H{"error": 1})
		return
	}
	c.JSON(http.StatusOK, gin.H{"error": 0})
}

// requestBase 从请求推断出本服务的对外地址，用于拼回调 URL。
func requestBase(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if v := c.GetHeader("X-Forwarded-Proto"); v != "" {
		scheme = v
	}
	host := c.Request.Host
	if v := c.GetHeader("X-Forwarded-Host"); v != "" {
		host = v
	}
	return scheme + "://" + host
}
