package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// ruleView 是权限规则的展示视图，补上授权对象的名字。
type ruleView struct {
	model.AccessRule
	PrincipalName string   `json:"principal_name"`
	AllowCodes    []string `json:"allow_codes"`
	DenyCodes     []string `json:"deny_codes"`
	// Inherited 为 true 表示这条规则不是挂在当前目录上的，而是从上层继承来的。
	Inherited bool `json:"inherited"`
}

// ListACL 列出某个空间/目录上的权限设置（含继承而来的）。
func (h *Handler) ListACL(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	nodeID := uintQuery(c, "node_id", 0)
	if spaceID == 0 {
		response.Fail(c, response.BadRequest("请指定空间"))
		return
	}
	space, err := h.svc.Space.Get(spaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(spaceID, nodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	// 能看到权限清单本身就是一种敏感能力，要求 manage。
	if _, err := h.svc.ACL.Require(subj, space, node, model.PermManage); err != nil {
		response.Fail(c, err)
		return
	}

	direct, err := h.svc.ACL.ListRules(spaceID, nodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	inherited, err := h.svc.ACL.ListInheritedRules(spaceID, node)
	if err != nil {
		response.Fail(c, err)
		return
	}

	directViews, err := h.decorateRules(direct, false)
	if err != nil {
		response.Fail(c, err)
		return
	}
	inheritedViews, err := h.decorateRules(inherited, true)
	if err != nil {
		response.Fail(c, err)
		return
	}
	inherit := true
	if node != nil {
		inherit = !node.ACLIsolated
	}
	response.OK(c, gin.H{
		"direct":    directViews,
		"inherited": inheritedViews,
		"catalog":   model.PermissionCatalog(),
		"inherit":   inherit,
		// 空间根没有"上层"可继承，前端据此隐藏开关。
		"can_toggle_inherit": node != nil && node.IsDir,
	})
}

type inheritReq struct {
	SpaceID uint64 `json:"space_id"`
	NodeID  uint64 `json:"node_id"`
	Inherit bool   `json:"inherit"`
}

// SetInheritance 打开或切断某个目录的权限继承。
//
// 切断继承是"这个目录只给某几个人"的正确做法——用拒绝规则去挡部门会把
// 想放行的人一起挡住，因为拒绝优先于一切允许。
func (h *Handler) SetInheritance(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[inheritReq](c)
	if !ok {
		return
	}
	space, err := h.svc.Space.Get(req.SpaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(req.SpaceID, req.NodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if node == nil {
		response.Fail(c, response.BadRequest("空间根目录不能切断继承"))
		return
	}
	if _, err := h.svc.ACL.Require(subj, space, node, model.PermManage); err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.ACL.SetInheritance(node, req.Inherit); err != nil {
		response.Fail(c, err)
		return
	}
	detail := "切断继承"
	if req.Inherit {
		detail = "恢复继承"
	}
	h.audit(c, service.ActionACLGrant, "acl", node.ID, node.Name, detail, true)
	response.OK(c, gin.H{"inherit": req.Inherit})
}

func (h *Handler) decorateRules(rules []model.AccessRule, inherited bool) ([]ruleView, error) {
	out := make([]ruleView, 0, len(rules))
	userIDs := make([]uint64, 0)
	deptIDs := make([]uint64, 0)
	for _, r := range rules {
		switch r.PrincipalType {
		case model.PrincipalUser:
			userIDs = append(userIDs, r.PrincipalID)
		case model.PrincipalDept:
			deptIDs = append(deptIDs, r.PrincipalID)
		}
	}

	userNames := map[uint64]string{}
	if len(userIDs) > 0 {
		var users []model.User
		if err := h.svc.DB.Select("id", "username", "nickname").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			name := u.Nickname
			if name == "" {
				name = u.Username
			}
			userNames[u.ID] = name + "（" + u.Username + "）"
		}
	}
	deptNames := map[uint64]string{}
	if len(deptIDs) > 0 {
		var depts []model.Department
		if err := h.svc.DB.Select("id", "name").Where("id IN ?", deptIDs).Find(&depts).Error; err != nil {
			return nil, err
		}
		for _, d := range depts {
			deptNames[d.ID] = d.Name
		}
	}

	for _, r := range rules {
		name := ""
		switch r.PrincipalType {
		case model.PrincipalUser:
			name = userNames[r.PrincipalID]
			if name == "" {
				name = "已删除的用户"
			}
		case model.PrincipalDept:
			name = deptNames[r.PrincipalID]
			if name == "" {
				name = "已删除的部门"
			}
			if r.IncludeSubDept {
				name += "（含子部门）"
			}
		case model.PrincipalRole:
			name = r.PrincipalRole.Label()
		case model.PrincipalEveryone:
			name = "全体成员"
		}
		out = append(out, ruleView{
			AccessRule:    r,
			PrincipalName: name,
			AllowCodes:    r.Allow.Codes(),
			DenyCodes:     r.Deny.Codes(),
			Inherited:     inherited,
		})
	}
	return out, nil
}

type grantReq struct {
	SpaceID        uint64              `json:"space_id"`
	NodeID         uint64              `json:"node_id"`
	PrincipalType  model.PrincipalType `json:"principal_type"`
	PrincipalID    uint64              `json:"principal_id"`
	PrincipalRole  model.Role          `json:"principal_role"`
	Allow          []string            `json:"allow"`
	Deny           []string            `json:"deny"`
	IncludeSubDept bool                `json:"include_sub_dept"`
	Inheritable    bool                `json:"inheritable"`
	// ExpireDays 为 0 表示长期有效。
	ExpireDays int    `json:"expire_days"`
	Remark     string `json:"remark"`
}

// Grant 新增或更新一条授权。
func (h *Handler) Grant(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[grantReq](c)
	if !ok {
		return
	}
	space, err := h.svc.Space.Get(req.SpaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(req.SpaceID, req.NodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	granterPerm, err := h.svc.ACL.Require(subj, space, node, model.PermManage)
	if err != nil {
		response.Fail(c, err)
		return
	}

	allow := model.ParsePermissions(req.Allow).Normalize()
	// 不能授出自己都没有的权限，否则"授权管理"就成了提权入口。
	if !subj.IsSuperAdmin() && !granterPerm.Has(allow) {
		response.Fail(c, response.Forbidden("不能授予超出你自身权限的能力"))
		return
	}

	rule := &model.AccessRule{
		SpaceID:        req.SpaceID,
		NodeID:         req.NodeID,
		PrincipalType:  req.PrincipalType,
		PrincipalID:    req.PrincipalID,
		PrincipalRole:  req.PrincipalRole,
		Allow:          allow,
		Deny:           model.ParsePermissions(req.Deny),
		IncludeSubDept: req.IncludeSubDept,
		Inheritable:    req.Inheritable,
		Remark:         req.Remark,
		CreatedBy:      subj.User.ID,
	}
	if req.ExpireDays > 0 {
		exp := time.Now().AddDate(0, 0, req.ExpireDays)
		rule.ExpireAt = &exp
	}
	if err := h.svc.ACL.SaveRule(rule); err != nil {
		h.audit(c, service.ActionACLGrant, "acl", req.NodeID, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionACLGrant, "acl", rule.ID, "",
		string(rule.PrincipalType)+" 允许="+rule.Allow.String()+" 拒绝="+rule.Deny.String(), true)
	response.OK(c, rule)
}

// Revoke 删除一条授权。
func (h *Handler) Revoke(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	var rule model.AccessRule
	if err := h.svc.DB.First(&rule, id).Error; err != nil {
		response.Fail(c, response.NotFound("权限规则不存在"))
		return
	}
	space, err := h.svc.Space.Get(rule.SpaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(rule.SpaceID, rule.NodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if _, err := h.svc.ACL.Require(subj, space, node, model.PermManage); err != nil {
		response.Fail(c, err)
		return
	}
	if err := h.svc.ACL.DeleteRule(id); err != nil {
		h.audit(c, service.ActionACLRevoke, "acl", id, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionACLRevoke, "acl", id, "", "", true)
	response.OK(c, gin.H{"ok": true})
}

// MyPermissions 返回当前用户在某个节点上的实际权限，供前端控制按钮显隐。
func (h *Handler) MyPermissions(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	space, err := h.svc.Space.Get(spaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(spaceID, uintQuery(c, "node_id", 0))
	if err != nil {
		response.Fail(c, err)
		return
	}
	perm, err := h.svc.ACL.Effective(subj, space, node)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"perms": perm.Codes(), "value": uint32(perm)})
}
