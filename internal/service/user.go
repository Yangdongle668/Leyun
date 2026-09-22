package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// UserService 管理账号。
//
// 乐云没有注册入口：所有账号都由超级管理员开通。本服务里的 Create 也因此要求调用方
// 传入操作人并校验其身份，防止将来新增入口时绕过这条规则。
type UserService struct {
	db    *gorm.DB
	cfg   *config.Config
	space *SpaceService
	acl   *ACLService
}

// NewUserService 构造用户服务。
func NewUserService(db *gorm.DB, cfg *config.Config, space *SpaceService, acl *ACLService) *UserService {
	return &UserService{db: db, cfg: cfg, space: space, acl: acl}
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,31}$`)

// UserView 是返回给前端的用户视图。
type UserView struct {
	model.User
	RoleLabel    string `json:"role_label"`
	DeptName     string `json:"dept_name"`
	DeptPath     string `json:"dept_path"`
	SpaceID      uint64 `json:"space_id,omitempty"`
	UsedBytes    int64  `json:"used_bytes"`
	CreatorName  string `json:"creator_name,omitempty"`
	StatusLabel  string `json:"status_label"`
	QuotaDisplay string `json:"quota_display"`
}

// Get 按 ID 读取用户。
func (s *UserService) Get(id uint64) (*model.User, error) {
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("用户不存在")
		}
		return nil, fmt.Errorf("加载用户失败: %w", err)
	}
	return &u, nil
}

// GetByUsername 按用户名读取用户。
func (s *UserService) GetByUsername(username string) (*model.User, error) {
	var u model.User
	if err := s.db.Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("用户不存在")
		}
		return nil, fmt.Errorf("加载用户失败: %w", err)
	}
	return &u, nil
}

// UserQuery 是用户列表的查询条件。
type UserQuery struct {
	Keyword string
	DeptID  uint64
	// IncludeSubDept 为 true 时按部门子树过滤。
	IncludeSubDept bool
	Role           string
	Status         string
	Page           int
	PageSize       int
	// ScopePath 限定只能看到该部门子树内的成员（部门管理员视角）。
	ScopePath string
}

// List 分页查询用户。
func (s *UserService) List(q UserQuery) ([]UserView, int64, error) {
	tx := s.db.Model(&model.User{})
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		tx = tx.Where("username LIKE ? OR nickname LIKE ? OR email LIKE ? OR phone LIKE ?", like, like, like, like)
	}
	if q.Role != "" {
		tx = tx.Where("role = ?", q.Role)
	}
	if q.Status != "" {
		tx = tx.Where("status = ?", q.Status)
	}

	scopeDeptIDs, err := s.scopeDeptIDs(q)
	if err != nil {
		return nil, 0, err
	}
	if scopeDeptIDs != nil {
		if len(scopeDeptIDs) == 0 {
			return []UserView{}, 0, nil
		}
		tx = tx.Where("dept_id IN ?", scopeDeptIDs)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计用户失败: %w", err)
	}
	page, size := normalizePage(q.Page, q.PageSize)
	var list []model.User
	err = tx.Order("id asc").Offset((page - 1) * size).Limit(size).Find(&list).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询用户失败: %w", err)
	}
	views, err := s.decorate(list)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// scopeDeptIDs 把"按部门筛选"与"部门管理员可见范围"合成一组部门 ID。
// 返回 nil 表示不限制部门。
func (s *UserService) scopeDeptIDs(q UserQuery) ([]uint64, error) {
	var byFilter, byScope []uint64

	if q.DeptID > 0 {
		if q.IncludeSubDept {
			d, err := s.deptByID(q.DeptID)
			if err != nil {
				return nil, err
			}
			ids, err := s.deptIDsUnder(d.Path)
			if err != nil {
				return nil, err
			}
			byFilter = ids
		} else {
			byFilter = []uint64{q.DeptID}
		}
	}
	if q.ScopePath != "" {
		ids, err := s.deptIDsUnder(q.ScopePath)
		if err != nil {
			return nil, err
		}
		byScope = ids
	}

	switch {
	case byFilter == nil && byScope == nil:
		return nil, nil
	case byFilter == nil:
		return byScope, nil
	case byScope == nil:
		return byFilter, nil
	default:
		allowed := make(map[uint64]struct{}, len(byScope))
		for _, id := range byScope {
			allowed[id] = struct{}{}
		}
		out := make([]uint64, 0, len(byFilter))
		for _, id := range byFilter {
			if _, ok := allowed[id]; ok {
				out = append(out, id)
			}
		}
		return out, nil
	}
}

func (s *UserService) deptByID(id uint64) (*model.Department, error) {
	var d model.Department
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("部门不存在")
		}
		return nil, fmt.Errorf("加载部门失败: %w", err)
	}
	return &d, nil
}

func (s *UserService) deptIDsUnder(path string) ([]uint64, error) {
	self := treex.SelfID(path)
	var ids []uint64
	tx := s.db.Model(&model.Department{}).Where("path LIKE ?", treex.LikePrefix(strings.TrimSuffix(path, "/")))
	if self > 0 {
		tx = tx.Or("id = ?", self)
	}
	if err := tx.Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("查询部门范围失败: %w", err)
	}
	return ids, nil
}

func (s *UserService) decorate(list []model.User) ([]UserView, error) {
	if len(list) == 0 {
		return []UserView{}, nil
	}
	deptIDs := make([]uint64, 0, len(list))
	userIDs := make([]uint64, 0, len(list))
	creatorIDs := make([]uint64, 0, len(list))
	for _, u := range list {
		deptIDs = append(deptIDs, u.DeptID)
		userIDs = append(userIDs, u.ID)
		if u.CreatedBy > 0 {
			creatorIDs = append(creatorIDs, u.CreatedBy)
		}
	}

	depts := map[uint64]model.Department{}
	if len(deptIDs) > 0 {
		var ds []model.Department
		if err := s.db.Where("id IN ?", deptIDs).Find(&ds).Error; err != nil {
			return nil, fmt.Errorf("查询部门失败: %w", err)
		}
		for _, d := range ds {
			depts[d.ID] = d
		}
	}
	spaces := map[uint64]model.Space{}
	var sps []model.Space
	if err := s.db.Where("type = ? AND owner_id IN ?", model.SpacePersonal, userIDs).Find(&sps).Error; err != nil {
		return nil, fmt.Errorf("查询个人空间失败: %w", err)
	}
	for _, sp := range sps {
		spaces[sp.OwnerID] = sp
	}
	creators := map[uint64]string{}
	if len(creatorIDs) > 0 {
		var cs []model.User
		if err := s.db.Select("id", "username", "nickname").Where("id IN ?", creatorIDs).Find(&cs).Error; err != nil {
			return nil, fmt.Errorf("查询创建人失败: %w", err)
		}
		for _, c := range cs {
			name := c.Nickname
			if name == "" {
				name = c.Username
			}
			creators[c.ID] = name
		}
	}

	// 部门全路径（"总公司 / 研发中心 / 平台组"）比单个部门名更好用。
	allDepts, err := s.allDeptNames()
	if err != nil {
		return nil, err
	}

	out := make([]UserView, 0, len(list))
	for _, u := range list {
		sp := spaces[u.ID]
		quota := "不限"
		if u.QuotaBytes > 0 {
			quota = HumanSize(u.QuotaBytes)
		}
		status := "正常"
		if u.Status == model.UserDisabled {
			status = "已停用"
		}
		view := UserView{
			User:         u,
			RoleLabel:    u.Role.Label(),
			DeptName:     depts[u.DeptID].Name,
			DeptPath:     deptFullName(allDepts, depts[u.DeptID].Path),
			SpaceID:      sp.ID,
			UsedBytes:    sp.UsedBytes,
			CreatorName:  creators[u.CreatedBy],
			StatusLabel:  status,
			QuotaDisplay: quota,
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *UserService) allDeptNames() (map[uint64]string, error) {
	var ds []model.Department
	if err := s.db.Select("id", "name").Find(&ds).Error; err != nil {
		return nil, fmt.Errorf("查询部门名称失败: %w", err)
	}
	out := make(map[uint64]string, len(ds))
	for _, d := range ds {
		out[d.ID] = d.Name
	}
	return out, nil
}

func deptFullName(names map[uint64]string, path string) string {
	ids := treex.IDs(path)
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := names[id]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, " / ")
}

// CreateUserInput 是开通账号的入参。
type CreateUserInput struct {
	Username string     `json:"username"`
	Password string     `json:"password"`
	Nickname string     `json:"nickname"`
	Email    string     `json:"email"`
	Phone    string     `json:"phone"`
	JobTitle string     `json:"job_title"`
	DeptID   uint64     `json:"dept_id"`
	Role     model.Role `json:"role"`
	Quota    int64      `json:"quota"`
	Remark   string     `json:"remark"`
	// MustChangePassword 为 true 时要求新用户首次登录必须改密。
	MustChangePassword bool `json:"must_change_password"`
}

// Create 开通账号。operator 必须是超级管理员——这是产品的硬约束。
func (s *UserService) Create(operator *model.User, in CreateUserInput, defaultQuota int64) (*model.User, error) {
	if operator == nil || !operator.IsSuperAdmin() {
		return nil, response.Forbidden("只有超级管理员可以开通账号")
	}
	in.Username = strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(in.Username) {
		return nil, response.BadRequest("用户名需为 2-32 位字母、数字、点、下划线或中划线，且以字母或数字开头")
	}
	if err := s.ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	if in.Role == "" {
		in.Role = model.RoleMember
	}
	if !in.Role.Valid() {
		return nil, response.BadRequest("非法的角色")
	}
	if in.DeptID == 0 {
		return nil, response.BadRequest("请为账号指定所属部门")
	}
	if _, err := s.deptByID(in.DeptID); err != nil {
		return nil, err
	}

	var exists int64
	if err := s.db.Model(&model.User{}).Where("username = ?", in.Username).Count(&exists).Error; err != nil {
		return nil, fmt.Errorf("检查用户名失败: %w", err)
	}
	if exists > 0 {
		return nil, response.Conflict("用户名已存在")
	}

	hashed, err := hashx.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = in.Username
	}
	quota := in.Quota
	if quota == 0 {
		quota = defaultQuota
	}

	u := &model.User{
		Username:           in.Username,
		PasswordHash:       hashed,
		Nickname:           nickname,
		Email:              strings.TrimSpace(in.Email),
		Phone:              strings.TrimSpace(in.Phone),
		JobTitle:           strings.TrimSpace(in.JobTitle),
		DeptID:             in.DeptID,
		Role:               in.Role,
		Status:             model.UserActive,
		QuotaBytes:         quota,
		MustChangePassword: in.MustChangePassword,
		CreatedBy:          operator.ID,
		Remark:             in.Remark,
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}
	// 个人空间随账号一起建好，用户登录后立刻有地方放文件。
	sp, err := s.space.PersonalSpaceOf(u.ID, nickname)
	if err != nil {
		return nil, err
	}
	if quota > 0 && sp.QuotaBytes != quota {
		if err := s.space.UpdateQuota(sp.ID, quota); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// UpdateUserInput 是修改账号的入参。
type UpdateUserInput struct {
	Nickname *string     `json:"nickname"`
	Email    *string     `json:"email"`
	Phone    *string     `json:"phone"`
	JobTitle *string     `json:"job_title"`
	DeptID   *uint64     `json:"dept_id"`
	Role     *model.Role `json:"role"`
	Quota    *int64      `json:"quota"`
	Remark   *string     `json:"remark"`
	Status   *string     `json:"status"`
}

// Update 修改账号信息。
func (s *UserService) Update(operator *model.User, id uint64, in UpdateUserInput) (*model.User, error) {
	target, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if !operator.IsSuperAdmin() {
		return nil, response.Forbidden("只有超级管理员可以修改账号")
	}

	updates := map[string]any{}
	if in.Nickname != nil {
		name := strings.TrimSpace(*in.Nickname)
		if name == "" {
			return nil, response.BadRequest("姓名不能为空")
		}
		updates["nickname"] = name
	}
	if in.Email != nil {
		updates["email"] = strings.TrimSpace(*in.Email)
	}
	if in.Phone != nil {
		updates["phone"] = strings.TrimSpace(*in.Phone)
	}
	if in.JobTitle != nil {
		updates["job_title"] = strings.TrimSpace(*in.JobTitle)
	}
	if in.Remark != nil {
		updates["remark"] = *in.Remark
	}
	if in.DeptID != nil && *in.DeptID != target.DeptID {
		if _, err := s.deptByID(*in.DeptID); err != nil {
			return nil, err
		}
		updates["dept_id"] = *in.DeptID
	}
	if in.Role != nil && *in.Role != target.Role {
		if !in.Role.Valid() {
			return nil, response.BadRequest("非法的角色")
		}
		if target.Role == model.RoleSuperAdmin && *in.Role != model.RoleSuperAdmin {
			if err := s.ensureNotLastSuperAdmin(target.ID); err != nil {
				return nil, err
			}
		}
		updates["role"] = *in.Role
	}
	if in.Status != nil {
		status := model.UserStatus(*in.Status)
		if status != model.UserActive && status != model.UserDisabled {
			return nil, response.BadRequest("非法的账号状态")
		}
		if status == model.UserDisabled {
			if target.ID == operator.ID {
				return nil, response.BadRequest("不能停用当前登录的账号")
			}
			if target.Role == model.RoleSuperAdmin {
				if err := s.ensureNotLastSuperAdmin(target.ID); err != nil {
					return nil, err
				}
			}
		}
		updates["status"] = status
		if status == model.UserActive {
			// 解禁时顺手清掉失败计数与锁定时间，省得管理员还要等锁过期。
			updates["login_failures"] = 0
			updates["locked_until"] = nil
		}
	}
	if in.Quota != nil {
		if *in.Quota < 0 {
			return nil, response.BadRequest("配额不能为负数")
		}
		updates["quota_bytes"] = *in.Quota
		sp, err := s.space.PersonalSpaceOf(target.ID, target.Nickname)
		if err != nil {
			return nil, err
		}
		if err := s.space.UpdateQuota(sp.ID, *in.Quota); err != nil {
			return nil, err
		}
	}

	if len(updates) > 0 {
		if err := s.db.Model(&model.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("更新用户失败: %w", err)
		}
	}
	return s.Get(id)
}

// UpdateSelf 允许用户修改自己的昵称与联系方式。
//
// 这里刻意只认三个字段：部门、角色、配额、状态都属于管理动作，必须走超管的 Update，
// 否则普通成员就能给自己升权。
func (s *UserService) UpdateSelf(id uint64, in UpdateUserInput) (*model.User, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	updates := map[string]any{}
	if in.Nickname != nil {
		name := strings.TrimSpace(*in.Nickname)
		if name == "" {
			return nil, response.BadRequest("姓名不能为空")
		}
		updates["nickname"] = name
	}
	if in.Email != nil {
		updates["email"] = strings.TrimSpace(*in.Email)
	}
	if in.Phone != nil {
		updates["phone"] = strings.TrimSpace(*in.Phone)
	}
	if len(updates) > 0 {
		if err := s.db.Model(&model.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("更新个人资料失败: %w", err)
		}
	}
	return s.Get(id)
}

// ensureNotLastSuperAdmin 保证系统里至少留一个可用的超级管理员，否则会把自己锁在门外。
func (s *UserService) ensureNotLastSuperAdmin(excludeID uint64) error {
	var count int64
	err := s.db.Model(&model.User{}).
		Where("role = ? AND status = ? AND id <> ?", model.RoleSuperAdmin, model.UserActive, excludeID).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("统计超级管理员失败: %w", err)
	}
	if count == 0 {
		return response.BadRequest("系统必须保留至少一个可用的超级管理员")
	}
	return nil
}

// ResetPassword 由超级管理员重置他人口令。
func (s *UserService) ResetPassword(operator *model.User, id uint64, newPassword string, mustChange bool) error {
	if !operator.IsSuperAdmin() {
		return response.Forbidden("只有超级管理员可以重置口令")
	}
	if _, err := s.Get(id); err != nil {
		return err
	}
	if err := s.ValidatePassword(newPassword); err != nil {
		return err
	}
	hashed, err := hashx.HashPassword(newPassword)
	if err != nil {
		return err
	}
	err = s.db.Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{
		"password_hash":        hashed,
		"must_change_password": mustChange,
		"login_failures":       0,
		"locked_until":         nil,
	}).Error
	if err != nil {
		return fmt.Errorf("重置口令失败: %w", err)
	}
	return nil
}

// ChangePassword 由用户本人改密。
func (s *UserService) ChangePassword(userID uint64, oldPassword, newPassword string) error {
	u, err := s.Get(userID)
	if err != nil {
		return err
	}
	if !hashx.VerifyPassword(u.PasswordHash, oldPassword) {
		return response.BadRequest("原口令不正确")
	}
	if err := s.ValidatePassword(newPassword); err != nil {
		return err
	}
	if hashx.VerifyPassword(u.PasswordHash, newPassword) {
		return response.BadRequest("新口令不能与原口令相同")
	}
	hashed, err := hashx.HashPassword(newPassword)
	if err != nil {
		return err
	}
	err = s.db.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"password_hash":        hashed,
		"must_change_password": false,
	}).Error
	if err != nil {
		return fmt.Errorf("修改口令失败: %w", err)
	}
	return nil
}

// ValidatePassword 校验口令强度。
func (s *UserService) ValidatePassword(pwd string) error {
	if len(pwd) < s.cfg.Security.PasswordMinLength {
		return response.BadRequest(fmt.Sprintf("口令长度至少 %d 位", s.cfg.Security.PasswordMinLength))
	}
	if len(pwd) > 128 {
		return response.BadRequest("口令过长")
	}
	return nil
}

// Delete 删除账号。个人空间内还有文件时拒绝，避免数据无声消失。
func (s *UserService) Delete(operator *model.User, id uint64) error {
	if !operator.IsSuperAdmin() {
		return response.Forbidden("只有超级管理员可以删除账号")
	}
	if operator.ID == id {
		return response.BadRequest("不能删除当前登录的账号")
	}
	target, err := s.Get(id)
	if err != nil {
		return err
	}
	if target.Role == model.RoleSuperAdmin {
		if err := s.ensureNotLastSuperAdmin(id); err != nil {
			return err
		}
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var sp model.Space
		err := tx.Where("type = ? AND owner_id = ?", model.SpacePersonal, id).First(&sp).Error
		if err == nil {
			var nodeCount int64
			if err := tx.Model(&model.Node{}).Where("space_id = ?", sp.ID).Count(&nodeCount).Error; err != nil {
				return fmt.Errorf("统计个人空间文件失败: %w", err)
			}
			if nodeCount > 0 {
				return response.Conflict("该账号的个人空间内仍有文件，请先转移或清空")
			}
			if err := tx.Where("space_id = ?", sp.ID).Delete(&model.AccessRule{}).Error; err != nil {
				return fmt.Errorf("清理个人空间权限失败: %w", err)
			}
			if err := tx.Delete(&model.Space{}, sp.ID).Error; err != nil {
				return fmt.Errorf("删除个人空间失败: %w", err)
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("查询个人空间失败: %w", err)
		}

		if err := tx.Where("principal_type = ? AND principal_id = ?", model.PrincipalUser, id).
			Delete(&model.AccessRule{}).Error; err != nil {
			return fmt.Errorf("清理用户授权失败: %w", err)
		}
		if err := tx.Where("created_by = ?", id).Delete(&model.Share{}).Error; err != nil {
			return fmt.Errorf("清理用户分享失败: %w", err)
		}
		if err := tx.Delete(&model.User{}, id).Error; err != nil {
			return fmt.Errorf("删除用户失败: %w", err)
		}
		return nil
	})
}

// Search 按关键字搜索用户，用于授权对话框的"选人"。
func (s *UserService) Search(keyword string, limit int) ([]UserView, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	tx := s.db.Model(&model.User{}).Where("status = ?", model.UserActive)
	if keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where("username LIKE ? OR nickname LIKE ?", like, like)
	}
	var list []model.User
	if err := tx.Order("id asc").Limit(limit).Find(&list).Error; err != nil {
		return nil, fmt.Errorf("搜索用户失败: %w", err)
	}
	return s.decorate(list)
}

// DecorateOne 把单个用户包装成视图。
func (s *UserService) DecorateOne(u *model.User) (*UserView, error) {
	views, err := s.decorate([]model.User{*u})
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, response.NotFound("用户不存在")
	}
	return &views[0], nil
}
