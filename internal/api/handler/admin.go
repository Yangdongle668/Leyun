package handler

import (
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// ===== 用户管理（仅超级管理员可写，部门管理员可读本部门） =====

// ListUsers 分页查询账号。
func (h *Handler) ListUsers(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	q := service.UserQuery{
		Keyword:        c.Query("keyword"),
		DeptID:         uintQuery(c, "dept_id", 0),
		IncludeSubDept: boolQuery(c, "include_sub", true),
		Role:           c.Query("role"),
		Status:         c.Query("status"),
		Page:           intQuery(c, "page", 1),
		PageSize:       intQuery(c, "page_size", 20),
	}
	// 部门管理员只能看到自己管辖子树内的人。
	if !subj.IsSuperAdmin() {
		if subj.ManagedDeptPath == "" {
			response.Fail(c, response.Forbidden("无权查看账号列表"))
			return
		}
		q.ScopePath = subj.ManagedDeptPath
	}

	list, total, err := h.svc.User.List(q)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total, q.Page, q.PageSize)
}

// CreateUser 开通账号。只有超级管理员能走到这里（路由上已加守卫，服务层再校验一次）。
func (h *Handler) CreateUser(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.CreateUserInput](c)
	if !ok {
		return
	}
	defaultQuota := h.svc.Setting.GetInt64(store.SettingDefaultUserQuota, h.svc.Cfg.Security.DefaultUserQuota)
	user, err := h.svc.User.Create(subj.User, *req, defaultQuota)
	if err != nil {
		h.audit(c, service.ActionUserCreate, "user", 0, req.Username, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionUserCreate, "user", user.ID, user.Username,
		"角色="+string(user.Role), true)
	view, err := h.svc.User.DecorateOne(user)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// GetUser 查询单个账号。
func (h *Handler) GetUser(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	u, err := h.svc.User.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	// 只校验列表接口是不够的：这里按 id 直接取，不挡住的话
	// 部门管理员挨个试 id 就能把全公司的人（含超管）的资料翻出来。
	if scope, ok2 := h.adminDeptScope(c, subj); !ok2 {
		return
	} else if scope != nil && !slices.Contains(scope, u.DeptID) {
		response.Fail(c, response.Forbidden("该账号不在你的管辖范围内"))
		return
	}
	view, err := h.svc.User.DecorateOne(u)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// UpdateUser 修改账号。
func (h *Handler) UpdateUser(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	req, ok := bind[service.UpdateUserInput](c)
	if !ok {
		return
	}
	user, err := h.svc.User.Update(subj.User, id, *req)
	if err != nil {
		h.audit(c, service.ActionUserUpdate, "user", id, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionUserUpdate, "user", user.ID, user.Username, "", true)
	view, err := h.svc.User.DecorateOne(user)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// DeleteUser 删除账号。
func (h *Handler) DeleteUser(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	target, err := h.svc.User.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.User.Delete(subj.User, id); err != nil {
		h.audit(c, service.ActionUserDelete, "user", id, target.Username, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionUserDelete, "user", id, target.Username, "", true)
	response.OK(c, gin.H{"ok": true})
}

type resetPasswordReq struct {
	NewPassword        string `json:"new_password"`
	MustChangePassword bool   `json:"must_change_password"`
}

// ResetUserPassword 由超管重置他人口令。
func (h *Handler) ResetUserPassword(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	req, ok := bind[resetPasswordReq](c)
	if !ok {
		return
	}
	target, err := h.svc.User.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.User.ResetPassword(subj.User, id, req.NewPassword, req.MustChangePassword); err != nil {
		h.audit(c, service.ActionUserResetPwd, "user", id, target.Username, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionUserResetPwd, "user", id, target.Username, "", true)
	response.OK(c, gin.H{"ok": true})
}

// SearchUsers 供授权对话框选人。
func (h *Handler) SearchUsers(c *gin.Context) {
	list, err := h.svc.User.Search(c.Query("keyword"), intQuery(c, "limit", 20))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ===== 部门管理 =====

// DepartmentTree 返回部门树。
func (h *Handler) DepartmentTree(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	rootPath := ""
	if !subj.IsSuperAdmin() && subj.ManagedDeptPath != "" {
		rootPath = subj.ManagedDeptPath
	}
	tree, err := h.svc.Dept.Tree(rootPath)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, tree)
}

// DepartmentList 返回平铺的部门列表，供下拉选择。
func (h *Handler) DepartmentList(c *gin.Context) {
	list, err := h.svc.Dept.List()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// CreateDepartment 新建部门。
func (h *Handler) CreateDepartment(c *gin.Context) {
	req, ok := bind[service.CreateDeptInput](c)
	if !ok {
		return
	}
	defaultQuota := h.svc.Setting.GetInt64(store.SettingDefaultDeptQuota, h.svc.Cfg.Security.DefaultDeptQuota)
	dept, err := h.svc.Dept.Create(*req, defaultQuota)
	if err != nil {
		h.audit(c, service.ActionDeptCreate, "dept", 0, req.Name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionDeptCreate, "dept", dept.ID, dept.Name, "", true)
	response.OK(c, dept)
}

// UpdateDepartment 修改部门。
func (h *Handler) UpdateDepartment(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	req, ok := bind[service.UpdateDeptInput](c)
	if !ok {
		return
	}
	dept, err := h.svc.Dept.Update(id, *req)
	if err != nil {
		h.audit(c, service.ActionDeptUpdate, "dept", id, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionDeptUpdate, "dept", dept.ID, dept.Name, "", true)
	response.OK(c, dept)
}

// DeleteDepartment 删除部门。
func (h *Handler) DeleteDepartment(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	dept, err := h.svc.Dept.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.Dept.Delete(id); err != nil {
		h.audit(c, service.ActionDeptDelete, "dept", id, dept.Name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionDeptDelete, "dept", id, dept.Name, "", true)
	response.OK(c, gin.H{"ok": true})
}

// ===== 审计与统计 =====

// ListAuditLogs 查询审计日志。
func (h *Handler) ListAuditLogs(c *gin.Context) {
	q := service.AuditQuery{
		Username: c.Query("username"),
		Action:   c.Query("action"),
		UserID:   uintQuery(c, "user_id", 0),
		DeptID:   uintQuery(c, "dept_id", 0),
		Keyword:  c.Query("keyword"),
		From:     c.Query("from"),
		To:       c.Query("to"),
		Page:     intQuery(c, "page", 1),
		PageSize: intQuery(c, "page_size", 20),
	}
	if raw := c.Query("success"); raw != "" {
		v := raw == "true" || raw == "1"
		q.Success = &v
	}
	// 部门管理员只能看自己管辖子树内的人做的事。
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	scope, ok := h.adminDeptScope(c, subj)
	if !ok {
		return
	}
	q.ScopeDeptIDs = scope

	list, total, err := h.svc.Audit.List(q)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total, q.Page, q.PageSize)
}

// AuditActions 返回出现过的审计动作。
func (h *Handler) AuditActions(c *gin.Context) {
	actions, err := h.svc.Audit.Actions()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, actions)
}

// Overview 返回管理后台概览。
//
// 部门管理员只看自己管辖的那棵子树。不限制的话，概览页会把全公司的
// 人数、部门数、空间用量都摊开——其中还包括别人的个人空间。
func (h *Handler) Overview(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	scope, ok := h.adminDeptScope(c, subj)
	if !ok {
		return
	}
	data, err := h.svc.Stats.Overview(scope)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}

// adminDeptScope 算出调用人在管理后台能看到的部门范围。
//
// 超级管理员返回 nil（不限）；部门管理员返回他管辖子树的部门 ID。
// 两者都不是的话直接回 403——能走到这里说明路由上的 anyAdmin 放行了，
// 但没有管辖范围的"管理员"不应该看到任何人的数据。
func (h *Handler) adminDeptScope(c *gin.Context, subj *service.Subject) ([]uint64, bool) {
	if subj.IsSuperAdmin() {
		return nil, true
	}
	if subj.ManagedDeptPath == "" {
		response.Fail(c, response.Forbidden("没有可管理的部门"))
		return nil, false
	}
	ids, err := h.svc.Dept.SubtreeIDs(treex.SelfID(subj.ManagedDeptPath))
	if err != nil {
		response.Fail(c, err)
		return nil, false
	}
	return ids, true
}

// ===== 系统设置 =====

// GetSettings 读取全部系统设置。
func (h *Handler) GetSettings(c *gin.Context) {
	// 必须是 AllSafe：All() 里带着令牌签名密钥和大模型 API Key，
	// 发给浏览器等于把它们交出去。
	all, err := h.svc.Setting.AllSafe()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"settings":           all,
		"password_min":       h.svc.Cfg.Security.PasswordMinLength,
		"chunk_size":         h.svc.Cfg.Storage.ChunkSize,
		"max_upload_size":    h.svc.Cfg.Storage.MaxUploadSize,
		"office_enabled":     h.svc.Office.Enabled(),
		"permission_catalog": model.PermissionCatalog(),
	})
}

// UpdateSettings 更新系统设置。
func (h *Handler) UpdateSettings(c *gin.Context) {
	req, ok := bind[map[string]string](c)
	if !ok {
		return
	}
	if err := h.svc.Setting.SetMany(*req); err != nil {
		response.Fail(c, response.BadRequest(err.Error()))
		return
	}
	h.audit(c, service.ActionSettingUpdate, "setting", 0, "", "", true)
	all, err := h.svc.Setting.AllSafe()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, all)
}

// ===== 空间管理 =====

// ListAllSpaces 列出全部空间（管理后台用）。
func (h *Handler) ListAllSpaces(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	list, err := h.svc.Space.VisibleSpaces(subj)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

type updateSpaceReq struct {
	Name  *string `json:"name"`
	Quota *int64  `json:"quota"`
}

// UpdateSpace 修改空间名称与配额。
func (h *Handler) UpdateSpace(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	req, ok := bind[updateSpaceReq](c)
	if !ok {
		return
	}
	sp, err := h.svc.Space.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if req.Name != nil {
		if err := h.svc.Space.Rename(id, *req.Name); err != nil {
			response.Fail(c, err)
			return
		}
	}
	if req.Quota != nil {
		if err := h.svc.Space.UpdateQuota(id, *req.Quota); err != nil {
			response.Fail(c, err)
			return
		}
	}
	h.audit(c, service.ActionSpaceUpdate, "space", id, sp.Name, "", true)
	updated, err := h.svc.Space.Get(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, updated)
}
