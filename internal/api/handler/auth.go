package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/api/middleware"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login 处理登录。
func (h *Handler) Login(c *gin.Context) {
	req, ok := bind[loginReq](c)
	if !ok {
		return
	}
	res, err := h.svc.Auth.Login(req.Username, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		response.Fail(c, err)
		return
	}
	view, err := h.svc.User.DecorateOne(res.User)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"token":               res.Token,
		"expire_at":           res.ExpireAt,
		"user":                view,
		"must_reset_password": res.MustReset,
		"permissions":         model.PermissionCatalog(),
	})
}

// Logout 处理登出。服务端不维护会话，这里只写一条审计。
func (h *Handler) Logout(c *gin.Context) {
	h.audit(c, service.ActionLogout, "user", 0, "", "", true)
	response.OK(c, gin.H{"ok": true})
}

// Profile 返回当前登录用户的档案与可见空间。
func (h *Handler) Profile(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	view, err := h.svc.User.DecorateOne(subj.User)
	if err != nil {
		response.Fail(c, err)
		return
	}
	spaces, err := h.svc.Space.VisibleSpaces(subj)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"user":                view,
		"spaces":              spaces,
		"permissions":         model.PermissionCatalog(),
		"is_super_admin":      subj.IsSuperAdmin(),
		"must_reset_password": subj.User.MustChangePassword,
		"office_enabled":      h.svc.Office.Enabled(),
		"settings":            h.svc.Setting.Public(),
	})
}

// Refresh 续签令牌。
func (h *Handler) Refresh(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	res, err := h.svc.Auth.Refresh(subj.User)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"token": res.Token, "expire_at": res.ExpireAt})
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword 处理本人改密。
func (h *Handler) ChangePassword(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[changePasswordReq](c)
	if !ok {
		return
	}
	if err := h.svc.User.ChangePassword(subj.User.ID, req.OldPassword, req.NewPassword); err != nil {
		h.audit(c, service.ActionChangePwd, "user", subj.User.ID, subj.User.Username, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionChangePwd, "user", subj.User.ID, subj.User.Username, "", true)
	response.OK(c, gin.H{"ok": true})
}

type updateProfileReq struct {
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

// UpdateProfile 修改本人的昵称与联系方式。部门、角色、配额只能由超管改。
func (h *Handler) UpdateProfile(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[updateProfileReq](c)
	if !ok {
		return
	}
	in := service.UpdateUserInput{}
	if req.Nickname != "" {
		in.Nickname = &req.Nickname
	}
	in.Email = &req.Email
	in.Phone = &req.Phone

	// 走一条不经过超管校验的窄通道：只允许改这三个字段。
	updated, err := h.svc.User.UpdateSelf(subj.User.ID, in)
	if err != nil {
		response.Fail(c, err)
		return
	}
	view, err := h.svc.User.DecorateOne(updated)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// SiteInfo 返回登录页需要的公开信息。
func (h *Handler) SiteInfo(c *gin.Context) {
	response.OK(c, gin.H{
		"settings":        h.svc.Setting.Public(),
		"allow_register":  false,
		"register_notice": "本系统不开放自助注册，账号由超级管理员统一开通",
		"office_enabled":  h.svc.Office.Enabled(),
	})
}

// Me 返回极简的当前用户信息，供前端轮询判活。
func (h *Handler) Me(c *gin.Context) {
	u := middleware.CurrentUser(c)
	if u == nil {
		response.Fail(c, response.Unauthorized("请先登录"))
		return
	}
	response.OK(c, gin.H{
		"id": u.ID, "username": u.Username, "nickname": u.Nickname,
		"role": u.Role, "role_label": u.Role.Label(),
	})
}
