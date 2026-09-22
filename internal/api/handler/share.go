package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/api/middleware"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// CreateShare 创建分享。
func (h *Handler) CreateShare(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.CreateShareInput](c)
	if !ok {
		return
	}
	allowPublic := h.svc.Setting.GetBool(store.SettingAllowPublicShare, true)
	share, err := h.svc.Share.Create(subj, *req, allowPublic)
	if err != nil {
		h.audit(c, service.ActionShareCreate, "share", req.NodeID, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionShareCreate, "share", share.ID, share.Code, string(share.Scope), true)
	response.OK(c, share)
}

// ListShares 列出分享。
func (h *Handler) ListShares(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	page := intQuery(c, "page", 1)
	size := intQuery(c, "page_size", 20)
	list, total, err := h.svc.Share.List(subj, page, size, boolQuery(c, "mine", true))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total, page, size)
}

// RevokeShare 撤销分享。
func (h *Handler) RevokeShare(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Share.Revoke(subj, id); err != nil {
		h.audit(c, service.ActionShareRevoke, "share", id, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionShareRevoke, "share", id, "", "", true)
	response.OK(c, gin.H{"ok": true})
}

// openShare 是公开分享接口的公共入口：解析访客身份并打开分享。
func (h *Handler) openShare(c *gin.Context) (*service.ShareAccess, bool) {
	code := c.Param("code")
	if code == "" {
		code = c.Query("code")
	}
	password := c.Query("password")
	if password == "" {
		password = c.GetHeader("X-Share-Password")
	}
	viewer := middleware.CurrentSubject(c)

	access, err := h.svc.Share.Open(code, password, viewer)
	if err != nil {
		if errors.Is(err, service.ErrSharePassword) {
			// 用独立的业务码告诉前端"要弹提取码输入框"，而不是跳登录页。
			c.AbortWithStatusJSON(http.StatusOK, response.Body{
				Code: 42901, Message: "请输入提取码",
			})
			return nil, false
		}
		response.Fail(c, err)
		return nil, false
	}
	return access, true
}

// ShareInfo 返回分享的基本信息。
func (h *Handler) ShareInfo(c *gin.Context) {
	access, ok := h.openShare(c)
	if !ok {
		return
	}
	h.audit(c, service.ActionShareAccess, "share", access.Share.ID, access.Node.Name, "", true)
	response.OK(c, gin.H{
		"share": gin.H{
			"code":       access.Share.Code,
			"scope":      access.Share.Scope,
			"perms":      access.Share.Perms.Codes(),
			"expire_at":  access.Share.ExpireAt,
			"downloads":  access.Share.Downloads,
			"created_at": access.Share.CreatedAt,
		},
		"node": gin.H{
			"id":        access.Node.ID,
			"name":      access.Node.Name,
			"is_dir":    access.Node.IsDir,
			"size":      access.Node.Size,
			"size_text": service.HumanSize(access.Node.Size),
			"ext":       access.Node.Ext,
			"mime_type": access.Node.MimeType,
		},
		"space_name": access.Space.Name,
	})
}

// ShareList 列出分享目录下的内容。
func (h *Handler) ShareList(c *gin.Context) {
	access, ok := h.openShare(c)
	if !ok {
		return
	}
	items, parent, crumbs, err := h.svc.Share.ListShareChildren(access, uintQuery(c, "parent_id", 0))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "parent": parent, "crumbs": crumbs})
}

// ShareDownload 从分享中下载文件。
func (h *Handler) ShareDownload(c *gin.Context) {
	access, ok := h.openShare(c)
	if !ok {
		return
	}
	if !access.Share.Perms.Has(model.PermDownload) {
		response.Fail(c, response.Forbidden("该分享不允许下载"))
		return
	}
	node, err := h.svc.Share.ResolveFile(access, uintQuery(c, "node_id", 0))
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.Share.CountDownload(access.Share.ID); err != nil {
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionShareAccess, "share", access.Share.ID, node.Name, "下载", true)
	h.serveNode(c, node, true)
}

// SharePreview 在分享中预览文件。
func (h *Handler) SharePreview(c *gin.Context) {
	access, ok := h.openShare(c)
	if !ok {
		return
	}
	node, err := h.svc.Share.ResolveFile(access, uintQuery(c, "node_id", 0))
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.serveNode(c, node, false)
}
