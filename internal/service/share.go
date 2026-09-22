package service

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// ShareService 管理分享链接。
type ShareService struct {
	db    *gorm.DB
	cfg   *config.Config
	acl   *ACLService
	file  *FileService
	space *SpaceService
}

// NewShareService 构造分享服务。
func NewShareService(db *gorm.DB, cfg *config.Config, acl *ACLService, file *FileService, space *SpaceService) *ShareService {
	return &ShareService{db: db, cfg: cfg, acl: acl, file: file, space: space}
}

// CreateShareInput 是创建分享的入参。
type CreateShareInput struct {
	SpaceID uint64           `json:"space_id"`
	NodeID  uint64           `json:"node_id"`
	Scope   model.ShareScope `json:"scope"`
	Perms   []string         `json:"perms"`
	Passwd  string           `json:"password"`
	// ExpireDays 为 0 表示永久有效。
	ExpireDays   int             `json:"expire_days"`
	MaxDownloads int64           `json:"max_downloads"`
	Targets      []ShareTargetIn `json:"targets"`
}

// ShareTargetIn 是内部分享的可见对象。
type ShareTargetIn struct {
	Type model.PrincipalType `json:"type"`
	ID   uint64              `json:"id"`
}

// ShareView 是返回给前端的分享视图。
type ShareView struct {
	model.Share
	NodeName  string   `json:"node_name"`
	IsDir     bool     `json:"is_dir"`
	SpaceName string   `json:"space_name"`
	PermCodes []string `json:"perm_codes"`
	Expired   bool     `json:"expired"`
	Creator   string   `json:"creator,omitempty"`
}

// Create 创建一条分享。允许分享的权限只限于查看/下载/上传三项。
func (s *ShareService) Create(subj *Subject, in CreateShareInput, allowPublic bool) (*model.Share, error) {
	space, err := s.space.Get(in.SpaceID)
	if err != nil {
		return nil, err
	}
	node, err := s.file.GetNode(in.SpaceID, in.NodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, response.BadRequest("请选择要分享的文件或目录")
	}
	if node.Trashed {
		return nil, response.BadRequest("回收站中的条目不能分享")
	}
	if _, err := s.acl.Require(subj, space, node, model.PermShare); err != nil {
		return nil, err
	}

	if in.Scope == "" {
		in.Scope = model.ShareInternal
	}
	if in.Scope != model.ShareInternal && in.Scope != model.SharePublic {
		return nil, response.BadRequest("非法的分享范围")
	}
	if in.Scope == model.SharePublic && !allowPublic {
		return nil, response.Forbidden("系统已关闭对外公开分享")
	}

	perms := model.ParsePermissions(in.Perms)
	// 分享出去的能力不能超过分享者自己的能力，也不能超过分享允许的范围。
	perms &= model.PermView | model.PermDownload | model.PermUpload
	if perms == model.PermNone {
		perms = model.PermReadOnly
	}
	ownPerm, err := s.acl.Effective(subj, space, node)
	if err != nil {
		return nil, err
	}
	perms &= ownPerm
	if !perms.Has(model.PermView) {
		return nil, response.Forbidden("分享权限不能超出你自己拥有的权限")
	}
	if perms.Has(model.PermUpload) && !node.IsDir {
		perms &^= model.PermUpload
	}

	code, err := hashx.ShareCode(10)
	if err != nil {
		return nil, err
	}
	share := &model.Share{
		Code:         code,
		SpaceID:      in.SpaceID,
		NodeID:       in.NodeID,
		Scope:        in.Scope,
		Perms:        perms,
		MaxDownloads: in.MaxDownloads,
		CreatedBy:    subj.User.ID,
	}
	if in.Passwd != "" {
		hashed, err := hashx.HashPassword(in.Passwd)
		if err != nil {
			return nil, err
		}
		share.PasswordHash = hashed
	}
	if in.ExpireDays > 0 {
		exp := time.Now().AddDate(0, 0, in.ExpireDays)
		share.ExpireAt = &exp
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(share).Error; err != nil {
			return fmt.Errorf("创建分享失败: %w", err)
		}
		for _, t := range in.Targets {
			if !t.Type.Valid() || t.Type == model.PrincipalEveryone {
				continue
			}
			target := &model.ShareTarget{ShareID: share.ID, PrincipalType: t.Type, PrincipalID: t.ID}
			if err := tx.Create(target).Error; err != nil {
				return fmt.Errorf("设置分享对象失败: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	share.HasPassword = share.PasswordHash != ""
	return share, nil
}

// List 列出分享。超级管理员看全部，其他人只看自己创建的。
func (s *ShareService) List(subj *Subject, page, pageSize int, mineOnly bool) ([]ShareView, int64, error) {
	tx := s.db.Model(&model.Share{})
	if mineOnly || !subj.IsSuperAdmin() {
		tx = tx.Where("created_by = ?", subj.User.ID)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计分享失败: %w", err)
	}
	page, size := normalizePage(page, pageSize)
	var list []model.Share
	if err := tx.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("查询分享失败: %w", err)
	}
	views, err := s.decorate(list)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

func (s *ShareService) decorate(list []model.Share) ([]ShareView, error) {
	if len(list) == 0 {
		return []ShareView{}, nil
	}
	nodeIDs := make([]uint64, 0, len(list))
	spaceIDs := make([]uint64, 0, len(list))
	userIDs := make([]uint64, 0, len(list))
	for _, sh := range list {
		nodeIDs = append(nodeIDs, sh.NodeID)
		spaceIDs = append(spaceIDs, sh.SpaceID)
		userIDs = append(userIDs, sh.CreatedBy)
	}
	var nodes []model.Node
	if err := s.db.Select("id", "name", "is_dir").Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("查询分享节点失败: %w", err)
	}
	nodeMap := make(map[uint64]model.Node, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}
	var spaces []model.Space
	if err := s.db.Select("id", "name").Where("id IN ?", spaceIDs).Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("查询分享空间失败: %w", err)
	}
	spaceMap := make(map[uint64]string, len(spaces))
	for _, sp := range spaces {
		spaceMap[sp.ID] = sp.Name
	}
	var users []model.User
	if err := s.db.Select("id", "username", "nickname").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("查询分享创建人失败: %w", err)
	}
	userMap := make(map[uint64]string, len(users))
	for _, u := range users {
		name := u.Nickname
		if name == "" {
			name = u.Username
		}
		userMap[u.ID] = name
	}

	now := time.Now()
	out := make([]ShareView, 0, len(list))
	for _, sh := range list {
		sh.HasPassword = sh.PasswordHash != ""
		node := nodeMap[sh.NodeID]
		out = append(out, ShareView{
			Share:     sh,
			NodeName:  node.Name,
			IsDir:     node.IsDir,
			SpaceName: spaceMap[sh.SpaceID],
			PermCodes: sh.Perms.Codes(),
			Expired:   sh.ExpireAt != nil && sh.ExpireAt.Before(now),
			Creator:   userMap[sh.CreatedBy],
		})
	}
	return out, nil
}

// Revoke 撤销分享。
func (s *ShareService) Revoke(subj *Subject, id uint64) error {
	var share model.Share
	if err := s.db.First(&share, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound("分享不存在")
		}
		return fmt.Errorf("查询分享失败: %w", err)
	}
	if share.CreatedBy != subj.User.ID && !subj.IsSuperAdmin() {
		return response.Forbidden("只能撤销自己创建的分享")
	}
	if err := s.db.Model(&model.Share{}).Where("id = ?", id).Update("revoked", true).Error; err != nil {
		return fmt.Errorf("撤销分享失败: %w", err)
	}
	return nil
}

// ShareAccess 是访问分享后拿到的上下文。
type ShareAccess struct {
	Share *model.Share
	Node  *model.Node
	Space *model.Space
}

// ErrSharePassword 表示需要提供正确的提取码。
var ErrSharePassword = response.Unauthorized("请输入正确的提取码")

// Open 打开一个分享。viewer 为 nil 表示未登录的外部访客。
func (s *ShareService) Open(code, password string, viewer *Subject) (*ShareAccess, error) {
	var share model.Share
	err := s.db.Where("code = ?", code).First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("分享不存在或已被取消")
		}
		return nil, fmt.Errorf("查询分享失败: %w", err)
	}
	if share.Revoked {
		return nil, response.NotFound("分享已被取消")
	}
	if share.ExpireAt != nil && share.ExpireAt.Before(time.Now()) {
		return nil, response.NotFound("分享链接已过期")
	}
	if share.MaxDownloads > 0 && share.Downloads >= share.MaxDownloads {
		return nil, response.Forbidden("分享的下载次数已用完")
	}

	if share.Scope == model.ShareInternal {
		if viewer == nil || viewer.User == nil {
			return nil, response.Unauthorized("该分享仅限企业内部成员访问，请先登录")
		}
		ok, err := s.viewerAllowed(&share, viewer)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, response.Forbidden("你不在该分享的可见范围内")
		}
	}

	if share.PasswordHash != "" {
		// 分享创建者与超管本人打开时不必再输提取码。
		privileged := viewer != nil && viewer.User != nil &&
			(viewer.User.ID == share.CreatedBy || viewer.IsSuperAdmin())
		if !privileged && !hashx.VerifyPassword(share.PasswordHash, password) {
			return nil, ErrSharePassword
		}
	}

	node, err := s.file.GetNode(share.SpaceID, share.NodeID)
	if err != nil {
		return nil, err
	}
	if node == nil || node.Trashed {
		return nil, response.NotFound("分享的内容已被删除")
	}
	space, err := s.space.Get(share.SpaceID)
	if err != nil {
		return nil, err
	}

	share.HasPassword = share.PasswordHash != ""
	if err := s.db.Model(&model.Share{}).Where("id = ?", share.ID).
		UpdateColumn("views", gorm.Expr("views + 1")).Error; err != nil {
		return nil, fmt.Errorf("更新分享访问次数失败: %w", err)
	}
	return &ShareAccess{Share: &share, Node: node, Space: space}, nil
}

// viewerAllowed 判断内部分享是否对该用户可见。没有配置任何对象时对全员可见。
func (s *ShareService) viewerAllowed(share *model.Share, viewer *Subject) (bool, error) {
	if viewer.IsSuperAdmin() || viewer.User.ID == share.CreatedBy {
		return true, nil
	}
	var targets []model.ShareTarget
	if err := s.db.Where("share_id = ?", share.ID).Find(&targets).Error; err != nil {
		return false, fmt.Errorf("查询分享对象失败: %w", err)
	}
	if len(targets) == 0 {
		return true, nil
	}
	for _, t := range targets {
		switch t.PrincipalType {
		case model.PrincipalUser:
			if t.PrincipalID == viewer.User.ID {
				return true, nil
			}
		case model.PrincipalDept:
			if t.PrincipalID == viewer.SelfDeptID {
				return true, nil
			}
			for _, id := range viewer.AncestorDeptIDs {
				if id == t.PrincipalID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// ListShareChildren 列出分享目录下的内容。
func (s *ShareService) ListShareChildren(access *ShareAccess, parentID uint64) ([]NodeView, *model.Node, []Crumb, error) {
	if !access.Node.IsDir {
		return nil, nil, nil, response.BadRequest("该分享不是目录")
	}
	parent := access.Node
	if parentID != 0 && parentID != access.Node.ID {
		n, err := s.file.GetNode(access.Share.SpaceID, parentID)
		if err != nil {
			return nil, nil, nil, err
		}
		// 只能在分享的子树内浏览，不能借分享链接爬到上级目录。
		if n == nil || !n.IsDir || n.Trashed || !isUnder(n.Path, access.Node.Path) {
			return nil, nil, nil, response.NotFound("目录不存在")
		}
		parent = n
	}

	var items []model.Node
	err := s.db.Where("space_id = ? AND parent_id = ? AND trashed = ?", access.Share.SpaceID, parent.ID, false).
		Order("is_dir desc, name asc").Limit(2000).Find(&items).Error
	if err != nil {
		return nil, nil, nil, fmt.Errorf("查询分享内容失败: %w", err)
	}
	views := make([]NodeView, 0, len(items))
	for _, n := range items {
		views = append(views, s.file.toView(n, access.Share.Perms, ""))
	}

	crumbs := []Crumb{{ID: access.Node.ID, Name: access.Node.Name}}
	if parent.ID != access.Node.ID {
		all, err := s.file.Crumbs(parent)
		if err != nil {
			return nil, nil, nil, err
		}
		started := false
		crumbs = crumbs[:0]
		for _, c := range all {
			if c.ID == access.Node.ID {
				started = true
			}
			if started {
				crumbs = append(crumbs, c)
			}
		}
	}
	return views, parent, crumbs, nil
}

// ResolveFile 定位分享内的某个文件，用于下载与预览。
func (s *ShareService) ResolveFile(access *ShareAccess, nodeID uint64) (*model.Node, error) {
	if nodeID == 0 || nodeID == access.Node.ID {
		if access.Node.IsDir {
			return nil, response.BadRequest("请选择要下载的文件")
		}
		return access.Node, nil
	}
	n, err := s.file.GetNode(access.Share.SpaceID, nodeID)
	if err != nil {
		return nil, err
	}
	if n == nil || n.Trashed || !isUnder(n.Path, access.Node.Path) {
		return nil, response.NotFound("文件不存在")
	}
	if n.IsDir {
		return nil, response.BadRequest("目录不能直接下载")
	}
	return n, nil
}

// CountDownload 累计一次分享下载。
func (s *ShareService) CountDownload(shareID uint64) error {
	err := s.db.Model(&model.Share{}).Where("id = ?", shareID).
		UpdateColumn("downloads", gorm.Expr("downloads + 1")).Error
	if err != nil {
		return fmt.Errorf("更新分享下载次数失败: %w", err)
	}
	return nil
}

func isUnder(child, ancestor string) bool {
	return child == ancestor || len(child) > len(ancestor) && child[:len(ancestor)] == ancestor
}
