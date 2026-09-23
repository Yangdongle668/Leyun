package service

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// SpaceService 管理个人 / 部门 / 公共三类空间。
type SpaceService struct {
	db  *gorm.DB
	acl *ACLService
}

// NewSpaceService 构造空间服务。
func NewSpaceService(db *gorm.DB, acl *ACLService) *SpaceService {
	return &SpaceService{db: db, acl: acl}
}

// SpaceView 是返回给前端的空间视图，附带当前用户在该空间根上的权限。
type SpaceView struct {
	model.Space
	Perms     []string `json:"perms"`
	PermValue uint32   `json:"perm_value"`
	DeptName  string   `json:"dept_name,omitempty"`
	OwnerName string   `json:"owner_name,omitempty"`
}

// Get 按 ID 读取空间。
func (s *SpaceService) Get(id uint64) (*model.Space, error) {
	var sp model.Space
	if err := s.db.First(&sp, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("空间不存在")
		}
		return nil, fmt.Errorf("加载空间失败: %w", err)
	}
	return &sp, nil
}

// PersonalSpaceOf 返回用户的个人空间，不存在时自动补建。
func (s *SpaceService) PersonalSpaceOf(userID uint64, nickname string) (*model.Space, error) {
	var sp model.Space
	err := s.db.Where("type = ? AND owner_id = ?", model.SpacePersonal, userID).First(&sp).Error
	switch {
	case err == nil:
		return &sp, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		sp = model.Space{
			Type:    model.SpacePersonal,
			Name:    nickname + "的空间",
			OwnerID: userID,
			Enabled: true,
		}
		if err := s.db.Create(&sp).Error; err != nil {
			return nil, fmt.Errorf("创建个人空间失败: %w", err)
		}
		return &sp, nil
	default:
		return nil, fmt.Errorf("查询个人空间失败: %w", err)
	}
}

// DepartmentSpaceOf 返回部门空间，不存在时自动补建。
func (s *SpaceService) DepartmentSpaceOf(deptID uint64, deptName string, quota int64) (*model.Space, error) {
	var sp model.Space
	err := s.db.Where("type = ? AND dept_id = ?", model.SpaceDepartment, deptID).First(&sp).Error
	switch {
	case err == nil:
		return &sp, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		sp = model.Space{
			Type:       model.SpaceDepartment,
			Name:       deptName,
			DeptID:     deptID,
			QuotaBytes: quota,
			Enabled:    true,
		}
		if err := s.db.Create(&sp).Error; err != nil {
			return nil, fmt.Errorf("创建部门空间失败: %w", err)
		}
		// 新部门默认：本部门（含子部门）成员可读写，符合"部门空间是部门内共享盘"的直觉。
		rule := &model.AccessRule{
			SpaceID:        sp.ID,
			NodeID:         0,
			PrincipalType:  model.PrincipalDept,
			PrincipalID:    deptID,
			Allow:          model.PermCollaborate,
			IncludeSubDept: true,
			Inheritable:    true,
			Remark:         "本部门及子部门成员默认可读写并可分享",
		}
		if err := s.db.Create(rule).Error; err != nil {
			return nil, fmt.Errorf("初始化部门空间权限失败: %w", err)
		}
		return &sp, nil
	default:
		return nil, fmt.Errorf("查询部门空间失败: %w", err)
	}
}

// PublicSpace 返回公共空间（有多个时取 ID 最小的那个）。
func (s *SpaceService) PublicSpace() (*model.Space, error) {
	var sp model.Space
	err := s.db.Where("type = ?", model.SpacePublic).Order("id asc").First(&sp).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询公共空间失败: %w", err)
	}
	return &sp, nil
}

// VisibleSpaces 返回主体至少可以浏览的空间列表。
func (s *SpaceService) VisibleSpaces(subj *Subject) ([]SpaceView, error) {
	var spaces []model.Space

	if subj.IsSuperAdmin() {
		if err := s.db.Order("type asc, id asc").Find(&spaces).Error; err != nil {
			return nil, fmt.Errorf("查询空间列表失败: %w", err)
		}
	} else {
		candidates := map[uint64]struct{}{}
		// 1. 自己的个人空间。
		var own model.Space
		err := s.db.Where("type = ? AND owner_id = ?", model.SpacePersonal, subj.User.ID).First(&own).Error
		if err == nil {
			candidates[own.ID] = struct{}{}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("查询个人空间失败: %w", err)
		}
		// 2. 通过 ACL 拿到授权的空间。
		ids, err := s.acl.AccessibleSpaceIDs(subj)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			candidates[id] = struct{}{}
		}
		// 3. 部门管理员直接管辖子树内的部门空间。
		if subj.ManagedDeptPath != "" {
			var deptIDs []uint64
			err := s.db.Model(&model.Department{}).
				Where("path LIKE ?", subj.ManagedDeptPath+"%").
				Pluck("id", &deptIDs).Error
			if err != nil {
				return nil, fmt.Errorf("查询下属部门失败: %w", err)
			}
			if len(deptIDs) > 0 {
				var managed []model.Space
				if err := s.db.Where("type = ? AND dept_id IN ?", model.SpaceDepartment, deptIDs).Find(&managed).Error; err != nil {
					return nil, fmt.Errorf("查询部门空间失败: %w", err)
				}
				for _, sp := range managed {
					candidates[sp.ID] = struct{}{}
				}
			}
		}
		if len(candidates) == 0 {
			return []SpaceView{}, nil
		}
		idList := make([]uint64, 0, len(candidates))
		for id := range candidates {
			idList = append(idList, id)
		}
		if err := s.db.Where("id IN ?", idList).Order("type asc, id asc").Find(&spaces).Error; err != nil {
			return nil, fmt.Errorf("查询空间列表失败: %w", err)
		}
	}

	return s.decorate(subj, spaces)
}

// decorate 为空间补上权限位与部门/归属人名称，并过滤掉最终算下来看不见的空间。
func (s *SpaceService) decorate(subj *Subject, spaces []model.Space) ([]SpaceView, error) {
	if len(spaces) == 0 {
		return []SpaceView{}, nil
	}
	deptIDs := make([]uint64, 0)
	ownerIDs := make([]uint64, 0)
	for _, sp := range spaces {
		if sp.DeptID > 0 {
			deptIDs = append(deptIDs, sp.DeptID)
		}
		if sp.OwnerID > 0 {
			ownerIDs = append(ownerIDs, sp.OwnerID)
		}
	}
	deptNames := map[uint64]string{}
	if len(deptIDs) > 0 {
		var depts []model.Department
		if err := s.db.Select("id", "name").Where("id IN ?", deptIDs).Find(&depts).Error; err != nil {
			return nil, fmt.Errorf("查询部门名称失败: %w", err)
		}
		for _, d := range depts {
			deptNames[d.ID] = d.Name
		}
	}
	ownerNames := map[uint64]string{}
	if len(ownerIDs) > 0 {
		var users []model.User
		if err := s.db.Select("id", "nickname", "username").Where("id IN ?", ownerIDs).Find(&users).Error; err != nil {
			return nil, fmt.Errorf("查询用户名称失败: %w", err)
		}
		for _, u := range users {
			name := u.Nickname
			if name == "" {
				name = u.Username
			}
			ownerNames[u.ID] = name
		}
	}

	out := make([]SpaceView, 0, len(spaces))
	for i := range spaces {
		sp := spaces[i]
		perm, err := s.acl.Effective(subj, &sp, nil)
		if err != nil {
			return nil, err
		}
		if !perm.Has(model.PermView) {
			// 空间根上没权限，不代表里头没有他能看的东西。
			// 跨部门授权常常只开放某个子目录（"这个文件夹给市场部看"），
			// 只看根的话这个空间不会出现在侧栏，被授权的目录就永远点不到。
			reachable, err := s.acl.CanReachInside(subj, &sp, nil)
			if err != nil {
				return nil, err
			}
			if !reachable {
				continue
			}
		}
		out = append(out, SpaceView{
			Space:     sp,
			Perms:     perm.Codes(),
			PermValue: uint32(perm),
			DeptName:  deptNames[sp.DeptID],
			OwnerName: ownerNames[sp.OwnerID],
		})
	}
	return out, nil
}

// AddUsage 调整空间已用容量，delta 可为负。
func (s *SpaceService) AddUsage(tx *gorm.DB, spaceID uint64, delta int64) error {
	if delta == 0 {
		return nil
	}
	err := tx.Model(&model.Space{}).Where("id = ?", spaceID).
		UpdateColumn("used_bytes", gorm.Expr("CASE WHEN used_bytes + ? < 0 THEN 0 ELSE used_bytes + ? END", delta, delta)).Error
	if err != nil {
		return fmt.Errorf("更新空间用量失败: %w", err)
	}
	return nil
}

// CheckQuota 判断再写入 size 字节是否会超出空间配额。
func (s *SpaceService) CheckQuota(space *model.Space, size int64) error {
	if space.QuotaBytes <= 0 || size <= 0 {
		return nil
	}
	if space.UsedBytes+size > space.QuotaBytes {
		return response.QuotaExceeded(fmt.Sprintf("空间容量不足，剩余 %s",
			HumanSize(max64(space.QuotaBytes-space.UsedBytes, 0))))
	}
	return nil
}

// UpdateQuota 修改空间配额。
func (s *SpaceService) UpdateQuota(spaceID uint64, quota int64) error {
	if quota < 0 {
		return response.BadRequest("配额不能为负数")
	}
	if err := s.db.Model(&model.Space{}).Where("id = ?", spaceID).Update("quota_bytes", quota).Error; err != nil {
		return fmt.Errorf("更新空间配额失败: %w", err)
	}
	return nil
}

// Rename 修改空间名称。
func (s *SpaceService) Rename(spaceID uint64, name string) error {
	if name == "" {
		return response.BadRequest("空间名称不能为空")
	}
	if err := s.db.Model(&model.Space{}).Where("id = ?", spaceID).Update("name", name).Error; err != nil {
		return fmt.Errorf("重命名空间失败: %w", err)
	}
	return nil
}

// HumanSize 把字节数格式化成便于阅读的字符串。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
