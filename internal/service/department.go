package service

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// DepartmentService 管理部门树。
type DepartmentService struct {
	db    *gorm.DB
	space *SpaceService
}

// NewDepartmentService 构造部门服务。
func NewDepartmentService(db *gorm.DB, space *SpaceService) *DepartmentService {
	return &DepartmentService{db: db, space: space}
}

// DeptNode 是带子节点与统计信息的部门视图。
type DeptNode struct {
	model.Department
	UserCount int64       `json:"user_count"`
	SpaceID   uint64      `json:"space_id,omitempty"`
	Children  []*DeptNode `json:"children"`
}

// Get 按 ID 读取部门。
func (s *DepartmentService) Get(id uint64) (*model.Department, error) {
	var d model.Department
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("部门不存在")
		}
		return nil, fmt.Errorf("加载部门失败: %w", err)
	}
	return &d, nil
}

// List 返回全部部门（平铺）。
func (s *DepartmentService) List() ([]model.Department, error) {
	var list []model.Department
	if err := s.db.Order("depth asc, sort asc, id asc").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("查询部门列表失败: %w", err)
	}
	return list, nil
}

// Tree 返回部门树。rootPath 非空时只返回该子树（部门管理员视角）。
func (s *DepartmentService) Tree(rootPath string) ([]*DeptNode, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	counts, err := s.userCounts()
	if err != nil {
		return nil, err
	}
	spaceIDs, err := s.spaceIDs()
	if err != nil {
		return nil, err
	}

	nodes := make(map[uint64]*DeptNode, len(list))
	filtered := make([]model.Department, 0, len(list))
	for _, d := range list {
		if rootPath != "" && !treex.IsDescendant(d.Path, rootPath) {
			continue
		}
		filtered = append(filtered, d)
	}
	for i := range filtered {
		d := filtered[i]
		nodes[d.ID] = &DeptNode{
			Department: d,
			UserCount:  counts[d.ID],
			SpaceID:    spaceIDs[d.ID],
			Children:   []*DeptNode{},
		}
	}

	roots := make([]*DeptNode, 0)
	for i := range filtered {
		d := filtered[i]
		node := nodes[d.ID]
		parent, ok := nodes[d.ParentID]
		if d.ParentID == 0 || !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, nil
}

func (s *DepartmentService) userCounts() (map[uint64]int64, error) {
	type row struct {
		DeptID uint64
		Cnt    int64
	}
	var rows []row
	err := s.db.Model(&model.User{}).
		Select("dept_id as dept_id, count(*) as cnt").
		Group("dept_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("统计部门人数失败: %w", err)
	}
	out := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		out[r.DeptID] = r.Cnt
	}
	return out, nil
}

func (s *DepartmentService) spaceIDs() (map[uint64]uint64, error) {
	var spaces []model.Space
	err := s.db.Select("id", "dept_id").Where("type = ?", model.SpaceDepartment).Find(&spaces).Error
	if err != nil {
		return nil, fmt.Errorf("查询部门空间失败: %w", err)
	}
	out := make(map[uint64]uint64, len(spaces))
	for _, sp := range spaces {
		out[sp.DeptID] = sp.ID
	}
	return out, nil
}

// SubtreeIDs 返回某部门及其全部下级部门的 ID。
func (s *DepartmentService) SubtreeIDs(deptID uint64) ([]uint64, error) {
	d, err := s.Get(deptID)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	err = s.db.Model(&model.Department{}).
		Where("path LIKE ?", treex.LikePrefix(strings.TrimSuffix(d.Path, "/"))).
		Or("id = ?", deptID).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查询下级部门失败: %w", err)
	}
	return ids, nil
}

// CreateDeptInput 是新建部门的入参。
type CreateDeptInput struct {
	ParentID uint64 `json:"parent_id"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	Sort     int    `json:"sort"`
	LeaderID uint64 `json:"leader_id"`
	Remark   string `json:"remark"`
	// Quota 部门空间配额，0 表示用系统默认值。
	Quota int64 `json:"quota"`
}

// Create 新建部门，并同步创建对应的部门空间。
func (s *DepartmentService) Create(in CreateDeptInput, defaultQuota int64) (*model.Department, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, response.BadRequest("部门名称不能为空")
	}
	var parentPath string
	var depth int
	if in.ParentID > 0 {
		parent, err := s.Get(in.ParentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	} else {
		parentPath = treex.Root
	}

	var dupe int64
	if err := s.db.Model(&model.Department{}).
		Where("parent_id = ? AND name = ?", in.ParentID, in.Name).Count(&dupe).Error; err != nil {
		return nil, fmt.Errorf("检查部门重名失败: %w", err)
	}
	if dupe > 0 {
		return nil, response.Conflict("同级下已存在同名部门")
	}

	dept := &model.Department{
		ParentID: in.ParentID,
		Name:     in.Name,
		Code:     strings.TrimSpace(in.Code),
		Depth:    depth,
		Sort:     in.Sort,
		LeaderID: in.LeaderID,
		Remark:   in.Remark,
		Enabled:  true,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(dept).Error; err != nil {
			return fmt.Errorf("创建部门失败: %w", err)
		}
		dept.Path = treex.Build(parentPath, dept.ID)
		if err := tx.Model(dept).Update("path", dept.Path).Error; err != nil {
			return fmt.Errorf("回写部门路径失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	quota := in.Quota
	if quota == 0 {
		quota = defaultQuota
	}
	if _, err := s.space.DepartmentSpaceOf(dept.ID, dept.Name, quota); err != nil {
		return nil, err
	}
	return dept, nil
}

// UpdateDeptInput 是修改部门的入参。
type UpdateDeptInput struct {
	Name     *string `json:"name"`
	Code     *string `json:"code"`
	Sort     *int    `json:"sort"`
	LeaderID *uint64 `json:"leader_id"`
	Remark   *string `json:"remark"`
	Enabled  *bool   `json:"enabled"`
	ParentID *uint64 `json:"parent_id"`
}

// Update 修改部门信息，支持调整上级部门（会同步重写整棵子树的路径）。
func (s *DepartmentService) Update(id uint64, in UpdateDeptInput) (*model.Department, error) {
	dept, err := s.Get(id)
	if err != nil {
		return nil, err
	}

	updates := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, response.BadRequest("部门名称不能为空")
		}
		updates["name"] = name
	}
	if in.Code != nil {
		updates["code"] = strings.TrimSpace(*in.Code)
	}
	if in.Sort != nil {
		updates["sort"] = *in.Sort
	}
	if in.LeaderID != nil {
		updates["leader_id"] = *in.LeaderID
	}
	if in.Remark != nil {
		updates["remark"] = *in.Remark
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if in.ParentID != nil && *in.ParentID != dept.ParentID {
			if err := s.moveSubtree(tx, dept, *in.ParentID, updates); err != nil {
				return err
			}
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.Department{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return fmt.Errorf("更新部门失败: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if in.Name != nil {
		// 部门空间的名字跟着部门走，避免改完部门名后空间还叫旧名字。
		if err := s.db.Model(&model.Space{}).
			Where("type = ? AND dept_id = ?", model.SpaceDepartment, id).
			Update("name", strings.TrimSpace(*in.Name)).Error; err != nil {
			return nil, fmt.Errorf("同步部门空间名称失败: %w", err)
		}
	}
	return s.Get(id)
}

// moveSubtree 把部门挂到新的上级，并批量重写子树路径。
func (s *DepartmentService) moveSubtree(tx *gorm.DB, dept *model.Department, newParentID uint64, updates map[string]any) error {
	newParentPath := treex.Root
	newDepth := 0
	if newParentID > 0 {
		var parent model.Department
		if err := tx.First(&parent, newParentID).Error; err != nil {
			return response.BadRequest("上级部门不存在")
		}
		if treex.IsDescendant(parent.Path, dept.Path) {
			return response.BadRequest("不能把部门移动到自己的下级里")
		}
		newParentPath = parent.Path
		newDepth = parent.Depth + 1
	}
	newPath := treex.Build(newParentPath, dept.ID)
	depthDelta := newDepth - dept.Depth

	// 先搬子孙：它们的路径前缀要整体替换。
	var descendants []model.Department
	if err := tx.Where("path LIKE ? AND id <> ?", treex.LikePrefix(strings.TrimSuffix(dept.Path, "/")), dept.ID).
		Find(&descendants).Error; err != nil {
		return fmt.Errorf("查询下级部门失败: %w", err)
	}
	for _, child := range descendants {
		rebased := treex.Rebase(child.Path, dept.Path, newPath)
		err := tx.Model(&model.Department{}).Where("id = ?", child.ID).
			Updates(map[string]any{"path": rebased, "depth": child.Depth + depthDelta}).Error
		if err != nil {
			return fmt.Errorf("更新下级部门路径失败: %w", err)
		}
	}

	updates["parent_id"] = newParentID
	updates["path"] = newPath
	updates["depth"] = newDepth
	return nil
}

// Delete 删除部门。存在下级部门或在职成员时拒绝，避免出现挂空的账号。
func (s *DepartmentService) Delete(id uint64) error {
	dept, err := s.Get(id)
	if err != nil {
		return err
	}
	if dept.ParentID == 0 {
		return response.BadRequest("顶级部门不允许删除")
	}
	var childCount int64
	if err := s.db.Model(&model.Department{}).Where("parent_id = ?", id).Count(&childCount).Error; err != nil {
		return fmt.Errorf("统计下级部门失败: %w", err)
	}
	if childCount > 0 {
		return response.Conflict("请先删除或移走下级部门")
	}
	var userCount int64
	if err := s.db.Model(&model.User{}).Where("dept_id = ?", id).Count(&userCount).Error; err != nil {
		return fmt.Errorf("统计部门成员失败: %w", err)
	}
	if userCount > 0 {
		return response.Conflict("该部门下仍有成员，请先调整成员所属部门")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var space model.Space
		err := tx.Where("type = ? AND dept_id = ?", model.SpaceDepartment, id).First(&space).Error
		if err == nil {
			var nodeCount int64
			if err := tx.Model(&model.Node{}).Where("space_id = ?", space.ID).Count(&nodeCount).Error; err != nil {
				return fmt.Errorf("统计部门空间文件失败: %w", err)
			}
			if nodeCount > 0 {
				return response.Conflict("该部门空间内仍有文件，请先清空后再删除部门")
			}
			if err := tx.Where("space_id = ?", space.ID).Delete(&model.AccessRule{}).Error; err != nil {
				return fmt.Errorf("清理部门空间权限失败: %w", err)
			}
			if err := tx.Delete(&model.Space{}, space.ID).Error; err != nil {
				return fmt.Errorf("删除部门空间失败: %w", err)
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("查询部门空间失败: %w", err)
		}

		// 清理授权给该部门的规则，避免留下指向已删部门的孤儿记录。
		if err := tx.Where("principal_type = ? AND principal_id = ?", model.PrincipalDept, id).
			Delete(&model.AccessRule{}).Error; err != nil {
			return fmt.Errorf("清理部门授权失败: %w", err)
		}
		if err := tx.Delete(&model.Department{}, id).Error; err != nil {
			return fmt.Errorf("删除部门失败: %w", err)
		}
		return nil
	})
}
