package service

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// Subject 是一次权限判定所需的主体快照。
//
// 把"这个人属于哪个部门、有哪些祖先部门"一次算好，后面判定多少个节点都不用再查库。
type Subject struct {
	User *model.User
	// SelfDeptID 用户直属部门。
	SelfDeptID uint64
	// AncestorDeptIDs 直属部门的全部祖先（不含自身）。授权给祖先部门且勾选了"包含子部门"时命中。
	AncestorDeptIDs []uint64
	// ManagedDeptPath 部门管理员所管辖子树的路径前缀；非部门管理员为空。
	ManagedDeptPath string
}

// IsSuperAdmin 判断主体是否超级管理员。
func (s *Subject) IsSuperAdmin() bool { return s.User != nil && s.User.IsSuperAdmin() }

// ManagesDept 判断该主体是否为 deptID 所在子树的部门管理员。
func (s *Subject) ManagesDept(deptPath string) bool {
	if s.ManagedDeptPath == "" {
		return false
	}
	return treex.IsDescendant(deptPath, s.ManagedDeptPath)
}

// ACLService 负责权限规则的存取与最终权限计算。
type ACLService struct {
	db *gorm.DB
}

// NewACLService 构造权限服务。
func NewACLService(db *gorm.DB) *ACLService { return &ACLService{db: db} }

// LoadSubject 根据用户装配权限主体。
func (s *ACLService) LoadSubject(user *model.User) (*Subject, error) {
	subj := &Subject{User: user, SelfDeptID: user.DeptID}
	if user.DeptID == 0 {
		return subj, nil
	}
	var dept model.Department
	if err := s.db.First(&dept, user.DeptID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 部门被删但用户还挂着旧 ID：按"无部门"处理，不阻断登录。
			return subj, nil
		}
		return nil, fmt.Errorf("加载用户部门失败: %w", err)
	}
	subj.AncestorDeptIDs = treex.AncestorIDs(dept.Path)
	if user.Role == model.RoleDeptAdmin {
		subj.ManagedDeptPath = dept.Path
	}
	return subj, nil
}

// matchesPrincipal 判断一条规则是否命中当前主体。
func (s *Subject) matchesPrincipal(rule *model.AccessRule) bool {
	switch rule.PrincipalType {
	case model.PrincipalEveryone:
		return true
	case model.PrincipalRole:
		return rule.PrincipalRole == s.User.Role
	case model.PrincipalUser:
		return rule.PrincipalID == s.User.ID
	case model.PrincipalDept:
		if rule.PrincipalID == 0 {
			return false
		}
		// 直属部门永远命中。
		if rule.PrincipalID == s.SelfDeptID {
			return true
		}
		// 上级部门只有在勾选"包含子部门"时才下放给子部门成员。
		if !rule.IncludeSubDept {
			return false
		}
		for _, id := range s.AncestorDeptIDs {
			if id == rule.PrincipalID {
				return true
			}
		}
		return false
	}
	return false
}

// applicable 判断规则在目标节点上是否生效。
//
// selfNodeID 是被判定节点自身的 ID（空间根为 0）。挂在节点自身的规则无条件生效；
// 来自空间根或祖先目录的规则则要求 Inheritable。
func applicable(rule *model.AccessRule, selfNodeID uint64, now time.Time) bool {
	if rule.ExpireAt != nil && !rule.ExpireAt.After(now) {
		return false
	}
	if rule.NodeID == selfNodeID {
		return true
	}
	return rule.Inheritable
}

// Effective 计算主体在指定节点上的最终权限。node 为 nil 表示空间根目录。
func (s *ACLService) Effective(subj *Subject, space *model.Space, node *model.Node) (model.Permission, error) {
	if subj == nil || subj.User == nil {
		return model.PermNone, nil
	}
	// 超级管理员是系统的最终兜底，任何 deny 都拦不住。
	if subj.IsSuperAdmin() {
		return model.PermAll, nil
	}
	if space == nil {
		return model.PermNone, nil
	}
	if !space.Enabled {
		return model.PermNone, nil
	}

	switch space.Type {
	case model.SpacePersonal:
		if space.OwnerID == subj.User.ID {
			return model.PermAll, nil
		}
		// 别人的个人空间默认完全不可见，只有对方显式授权才开一道口子。
	case model.SpaceDepartment:
		if subj.ManagedDeptPath != "" && space.DeptID > 0 {
			owns, err := s.deptManagedBy(subj, space.DeptID)
			if err != nil {
				return model.PermNone, err
			}
			if owns {
				return model.PermAll, nil
			}
		}
	}

	rules, err := s.rulesForNode(space.ID, node)
	if err != nil {
		return model.PermNone, err
	}

	var selfNodeID uint64
	if node != nil {
		selfNodeID = node.ID
	}
	now := time.Now()
	var allow, deny model.Permission
	for i := range rules {
		rule := &rules[i]
		if !applicable(rule, selfNodeID, now) {
			continue
		}
		if !subj.matchesPrincipal(rule) {
			continue
		}
		allow |= rule.Allow
		deny |= rule.Deny
	}
	// 显式拒绝优先：企业里"整个部门可读，唯独某人除外"必须能表达出来。
	return (allow &^ deny).Normalize() &^ deny, nil
}

// deptManagedBy 判断 deptID 是否落在部门管理员的管辖子树内。
func (s *ACLService) deptManagedBy(subj *Subject, deptID uint64) (bool, error) {
	if subj.ManagedDeptPath == "" {
		return false, nil
	}
	var dept model.Department
	if err := s.db.Select("id", "path").First(&dept, deptID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("加载部门失败: %w", err)
	}
	return treex.IsDescendant(dept.Path, subj.ManagedDeptPath), nil
}

// scopeNodeIDs 算出授权的收集范围：从节点自身向上，直到遇到第一个切断继承的目录为止。
//
// 没有切断时一路收到空间根（node_id = 0）；
// 某一级切断了，就到那一级为止，空间根与更上层的授权都传不进来。
func (s *ACLService) scopeNodeIDs(node *model.Node) ([]uint64, error) {
	if node == nil {
		return []uint64{0}, nil
	}
	chainIDs := treex.IDs(node.Path)
	if len(chainIDs) == 0 {
		return []uint64{0}, nil
	}

	var chain []model.Node
	err := s.db.Select("id", "acl_isolated").Where("id IN ?", chainIDs).Find(&chain).Error
	if err != nil {
		return nil, fmt.Errorf("加载目录继承设置失败: %w", err)
	}
	isolated := make(map[uint64]bool, len(chain))
	for _, n := range chain {
		isolated[n.ID] = n.ACLIsolated
	}

	out := make([]uint64, 0, len(chainIDs)+1)
	for i := len(chainIDs) - 1; i >= 0; i-- {
		id := chainIDs[i]
		out = append(out, id)
		if isolated[id] {
			// 到此为止：这一级自己的授权仍然算数，再往上的都不算。
			return out, nil
		}
	}
	return append(out, 0), nil
}

// rulesForNode 取出可能影响该节点的全部规则。
func (s *ACLService) rulesForNode(spaceID uint64, node *model.Node) ([]model.AccessRule, error) {
	nodeIDs, err := s.scopeNodeIDs(node)
	if err != nil {
		return nil, err
	}
	var rules []model.AccessRule
	if err := s.db.Where("space_id = ? AND node_id IN ?", spaceID, nodeIDs).Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("加载权限规则失败: %w", err)
	}
	return rules, nil
}

// SetInheritance 打开或切断某个目录的权限继承。
func (s *ACLService) SetInheritance(node *model.Node, inherit bool) error {
	if node == nil {
		return response.BadRequest("空间根目录不能切断继承")
	}
	if !node.IsDir {
		return response.BadRequest("只有目录可以设置继承")
	}
	err := s.db.Model(&model.Node{}).Where("id = ?", node.ID).
		Update("acl_isolated", !inherit).Error
	if err != nil {
		return fmt.Errorf("更新继承设置失败: %w", err)
	}
	node.ACLIsolated = !inherit
	return nil
}

// EffectiveForChildren 在已知父目录权限的前提下，批量算出一批子节点的权限。
//
// 列目录时逐个走 Effective 会产生 N 次查询；这里只补查挂在子节点自身上的规则。
func (s *ACLService) EffectiveForChildren(subj *Subject, space *model.Space, parentPerm model.Permission, children []model.Node) (map[uint64]model.Permission, error) {
	out := make(map[uint64]model.Permission, len(children))
	if len(children) == 0 {
		return out, nil
	}
	if subj.IsSuperAdmin() {
		for _, c := range children {
			out[c.ID] = model.PermAll
		}
		return out, nil
	}
	ids := make([]uint64, 0, len(children))
	for i := range children {
		c := &children[i]
		ids = append(ids, c.ID)
		if c.ACLIsolated {
			// 切断继承的目录不吃父级权限，只认挂在自己身上的规则。
			out[c.ID] = model.PermNone
		} else {
			out[c.ID] = parentPerm
		}
	}
	var rules []model.AccessRule
	if err := s.db.Where("space_id = ? AND node_id IN ?", space.ID, ids).Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("加载子节点权限规则失败: %w", err)
	}
	if len(rules) == 0 {
		return out, nil
	}

	now := time.Now()
	extraAllow := make(map[uint64]model.Permission, len(rules))
	extraDeny := make(map[uint64]model.Permission, len(rules))
	for i := range rules {
		rule := &rules[i]
		if rule.ExpireAt != nil && !rule.ExpireAt.After(now) {
			continue
		}
		if !subj.matchesPrincipal(rule) {
			continue
		}
		extraAllow[rule.NodeID] |= rule.Allow
		extraDeny[rule.NodeID] |= rule.Deny
	}
	for id, base := range out {
		merged := (base | extraAllow[id]) &^ extraDeny[id]
		out[id] = merged.Normalize() &^ extraDeny[id]
	}
	return out, nil
}

// Require 校验权限，不足时返回带中文提示的 403。
func (s *ACLService) Require(subj *Subject, space *model.Space, node *model.Node, want model.Permission) (model.Permission, error) {
	got, err := s.Effective(subj, space, node)
	if err != nil {
		return model.PermNone, err
	}
	if !got.Has(want) {
		if !got.Has(model.PermView) {
			return got, response.NotFound("文件或目录不存在")
		}
		return got, response.Forbidden(fmt.Sprintf("无“%s”权限", permLabels(want&^got)))
	}
	return got, nil
}

func permLabels(missing model.Permission) string {
	labels := map[string]string{
		"view": "查看", "download": "下载", "upload": "上传",
		"edit": "编辑", "delete": "删除", "share": "分享", "manage": "授权管理",
	}
	codes := missing.Codes()
	if len(codes) == 0 {
		return "操作"
	}
	out := ""
	for i, c := range codes {
		if i > 0 {
			out += "/"
		}
		out += labels[c]
	}
	return out
}

// AccessibleSpaceIDs 返回主体通过 ACL 拿到过授权的空间 ID（尚未扣除 deny）。
func (s *ACLService) AccessibleSpaceIDs(subj *Subject) ([]uint64, error) {
	q := s.db.Model(&model.AccessRule{}).
		Distinct("space_id").
		Where("allow <> 0").
		Where("expire_at IS NULL OR expire_at > ?", time.Now())

	// 组装"命中当前主体"的 SQL 条件，避免把全表规则拉回内存过滤。
	cond := s.db.Where("principal_type = ?", model.PrincipalEveryone)
	cond = cond.Or(s.db.Where("principal_type = ? AND principal_role = ?", model.PrincipalRole, subj.User.Role))
	cond = cond.Or(s.db.Where("principal_type = ? AND principal_id = ?", model.PrincipalUser, subj.User.ID))
	if subj.SelfDeptID > 0 {
		cond = cond.Or(s.db.Where("principal_type = ? AND principal_id = ?", model.PrincipalDept, subj.SelfDeptID))
	}
	if len(subj.AncestorDeptIDs) > 0 {
		cond = cond.Or(s.db.Where("principal_type = ? AND include_sub_dept = ? AND principal_id IN ?",
			model.PrincipalDept, true, subj.AncestorDeptIDs))
	}

	var ids []uint64
	if err := q.Where(cond).Pluck("space_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("查询可访问空间失败: %w", err)
	}
	return ids, nil
}

// ListRules 列出某个空间/节点上直接挂载的权限规则（不含继承来的）。
func (s *ACLService) ListRules(spaceID, nodeID uint64) ([]model.AccessRule, error) {
	var rules []model.AccessRule
	err := s.db.Where("space_id = ? AND node_id = ?", spaceID, nodeID).
		Order("principal_type asc, principal_id asc").Find(&rules).Error
	if err != nil {
		return nil, fmt.Errorf("查询权限规则失败: %w", err)
	}
	return rules, nil
}

// ListInheritedRules 列出实际继承下来的规则，供前端展示"权限从哪来"。
//
// 本目录切断了继承时返回空——界面上不该再列一堆其实不生效的规则误导人。
func (s *ACLService) ListInheritedRules(spaceID uint64, node *model.Node) ([]model.AccessRule, error) {
	if node == nil || node.ACLIsolated {
		return nil, nil
	}
	scope, err := s.scopeNodeIDs(node)
	if err != nil {
		return nil, err
	}
	// scope 的头一个是节点自身，那是"本级授权"，不算继承。
	ids := scope[1:]
	if len(ids) == 0 {
		return nil, nil
	}
	var rules []model.AccessRule
	err = s.db.Where("space_id = ? AND node_id IN ? AND inheritable = ?", spaceID, ids, true).
		Order("node_id asc").Find(&rules).Error
	if err != nil {
		return nil, fmt.Errorf("查询继承权限失败: %w", err)
	}
	return rules, nil
}

// SaveRule 新增或更新一条权限规则。同一个 (空间, 节点, 授权对象) 只保留一条，便于前端做"改权限"。
func (s *ACLService) SaveRule(rule *model.AccessRule) error {
	if !rule.PrincipalType.Valid() {
		return response.BadRequest("非法的授权对象类型")
	}
	if rule.PrincipalType == model.PrincipalRole && !rule.PrincipalRole.Valid() {
		return response.BadRequest("非法的角色名")
	}
	if rule.PrincipalType == model.PrincipalEveryone {
		rule.PrincipalID = 0
	}
	if (rule.PrincipalType == model.PrincipalUser || rule.PrincipalType == model.PrincipalDept) && rule.PrincipalID == 0 {
		return response.BadRequest("请选择授权对象")
	}
	rule.Allow = rule.Allow.Normalize()
	if rule.Allow == model.PermNone && rule.Deny == model.PermNone {
		return response.BadRequest("请至少选择一项权限")
	}

	q := s.db.Where("space_id = ? AND node_id = ? AND principal_type = ? AND principal_id = ?",
		rule.SpaceID, rule.NodeID, rule.PrincipalType, rule.PrincipalID)
	if rule.PrincipalType == model.PrincipalRole {
		q = q.Where("principal_role = ?", rule.PrincipalRole)
	}
	var existing model.AccessRule
	err := q.First(&existing).Error
	switch {
	case err == nil:
		existing.Allow = rule.Allow
		existing.Deny = rule.Deny
		existing.IncludeSubDept = rule.IncludeSubDept
		existing.Inheritable = rule.Inheritable
		existing.ExpireAt = rule.ExpireAt
		existing.Remark = rule.Remark
		if err := s.db.Save(&existing).Error; err != nil {
			return fmt.Errorf("更新权限规则失败: %w", err)
		}
		*rule = existing
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := s.db.Create(rule).Error; err != nil {
			return fmt.Errorf("创建权限规则失败: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("查询权限规则失败: %w", err)
	}
}

// DeleteRule 删除一条权限规则。
func (s *ACLService) DeleteRule(id uint64) error {
	res := s.db.Delete(&model.AccessRule{}, id)
	if res.Error != nil {
		return fmt.Errorf("删除权限规则失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return response.NotFound("权限规则不存在")
	}
	return nil
}
