package service

import (
	"errors"
	"fmt"
	"mime"
	"os"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
	"github.com/Yangdongle668/Leyun/internal/storage"
)

// FileService 管理空间内的目录树与文件。
type FileService struct {
	db    *gorm.DB
	cfg   *config.Config
	store *storage.Store
	acl   *ACLService
	space *SpaceService
}

// NewFileService 构造文件服务。
func NewFileService(db *gorm.DB, cfg *config.Config, store *storage.Store, acl *ACLService, space *SpaceService) *FileService {
	return &FileService{db: db, cfg: cfg, store: store, acl: acl, space: space}
}

// NodeView 是返回给前端的节点视图。
type NodeView struct {
	model.Node
	Perms       []string `json:"perms"`
	PermValue   uint32   `json:"perm_value"`
	SizeText    string   `json:"size_text"`
	CreatorName string   `json:"creator_name,omitempty"`
	// Editable 标记该文件可用在线 Office 编辑器打开。
	Editable bool `json:"editable"`
	// Previewable 标记浏览器可直接预览（图片/视频/PDF/文本）。
	Previewable bool `json:"previewable"`
	// IsPDF 标记走 PDF 链路：默认用内置 PDF.js 阅读器打开，
	// 需要改内容或填表单时再转到 ONLYOFFICE 的 PDF 编辑器。
	IsPDF bool `json:"is_pdf"`
}

// Crumb 是面包屑的一节。
type Crumb struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// ListResult 是目录列表的返回。
type ListResult struct {
	Space       *model.Space `json:"space"`
	Parent      *model.Node  `json:"parent"`
	Crumbs      []Crumb      `json:"crumbs"`
	Items       []NodeView   `json:"items"`
	ParentPerms []string     `json:"parent_perms"`
	Total       int64        `json:"total"`
}

// GetNode 读取节点并校验它确实属于该空间。
func (s *FileService) GetNode(spaceID, nodeID uint64) (*model.Node, error) {
	if nodeID == 0 {
		return nil, nil
	}
	var n model.Node
	if err := s.db.First(&n, nodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("文件或目录不存在")
		}
		return nil, fmt.Errorf("加载节点失败: %w", err)
	}
	if spaceID != 0 && n.SpaceID != spaceID {
		return nil, response.NotFound("文件或目录不存在")
	}
	return &n, nil
}

// List 列出目录内容。parentID 为 0 表示空间根目录。
func (s *FileService) List(subj *Subject, spaceID, parentID uint64, keyword, orderBy string) (*ListResult, error) {
	space, err := s.space.Get(spaceID)
	if err != nil {
		return nil, err
	}
	parent, err := s.GetNode(spaceID, parentID)
	if err != nil {
		return nil, err
	}
	if parent != nil {
		if !parent.IsDir {
			return nil, response.BadRequest("目标不是目录")
		}
		if parent.Trashed {
			return nil, response.NotFound("目录已在回收站中")
		}
	}

	parentPerm, err := s.acl.Require(subj, space, parent, model.PermView)
	if err != nil {
		// 这一层本身没权限，但下面可能挂着给他的授权（跨部门常见做法：
		// 只开放某一个子目录）。一律拒掉的话，他从侧栏点进来必然 403，
		// 而那个子目录明明是特意开给他的。
		//
		// 所以退一步：这一层按"零权限"处理放行，具体每个子项能不能看
		// 仍旧由下面的 EffectiveForChildren 逐个算——放行不等于给权限，
		// 没权限的子项照样不会出现在列表里。
		reachable, rErr := s.acl.CanReachInside(subj, space, parent)
		if rErr != nil {
			return nil, rErr
		}
		if !reachable {
			return nil, err
		}
		parentPerm = model.PermNone
	}

	tx := s.db.Model(&model.Node{}).Where("space_id = ? AND trashed = ?", spaceID, false)
	if keyword != "" {
		// 带关键字时在整个子树内搜索，而不仅仅是当前这一层。
		prefix := treex.Root
		if parent != nil {
			prefix = parent.Path
		}
		tx = tx.Where("path LIKE ?", treex.LikePrefix(strings.TrimSuffix(prefix, "/"))).
			Where("name LIKE ?", "%"+keyword+"%")
	} else {
		tx = tx.Where("parent_id = ?", parentID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计目录内容失败: %w", err)
	}

	var items []model.Node
	if err := tx.Order(nodeOrder(orderBy)).Limit(2000).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("查询目录内容失败: %w", err)
	}

	permMap, err := s.acl.EffectiveForChildren(subj, space, parentPerm, items)
	if err != nil {
		return nil, err
	}
	creators, err := s.creatorNames(items)
	if err != nil {
		return nil, err
	}

	// 自己没权限、但底下挂着给他的授权的目录，要留作"路过"用。
	// 否则单独授权一个深处的文件时，沿途每一级都会被下面那个过滤器抹掉，
	// 人就永远点不到那个文件。
	//
	// 只在确实有目录被挡下来时才去查：绝大多数情况下用户对整个目录都有权限，
	// 一个都不会被挡，这时候不该白白多跑两条查询。
	var blocked []model.Node
	for _, n := range items {
		if n.IsDir && !permMap[n.ID].Has(model.PermView) {
			blocked = append(blocked, n)
		}
	}
	passThrough := map[uint64]bool{}
	if len(blocked) > 0 {
		passThrough, err = s.acl.TraversableDirs(subj, spaceID, blocked)
		if err != nil {
			return nil, err
		}
	}

	views := make([]NodeView, 0, len(items))
	for _, n := range items {
		perm := permMap[n.ID]
		if !perm.Has(model.PermView) {
			if !passThrough[n.ID] {
				// 子目录上挂了 deny 的，直接从列表里消失，避免"看得见点不开"。
				continue
			}
			// 路过用的目录：一个权限都不给，界面上也就没有任何操作可做。
			perm = model.PermNone
		}
		views = append(views, s.toView(n, perm, creators[n.CreatedBy]))
	}

	crumbs, err := s.Crumbs(parent)
	if err != nil {
		return nil, err
	}
	return &ListResult{
		Space:       space,
		Parent:      parent,
		Crumbs:      crumbs,
		Items:       views,
		ParentPerms: parentPerm.Codes(),
		Total:       total,
	}, nil
}

func nodeOrder(orderBy string) string {
	// 目录永远排在文件前面，这是文件管理器的通用约定。
	switch orderBy {
	case "name_desc":
		return "is_dir desc, name desc"
	case "size":
		return "is_dir desc, size asc"
	case "size_desc":
		return "is_dir desc, size desc"
	case "time":
		return "is_dir desc, updated_at asc"
	case "time_desc":
		return "is_dir desc, updated_at desc"
	default:
		return "is_dir desc, name asc"
	}
}

func (s *FileService) toView(n model.Node, perm model.Permission, creator string) NodeView {
	return NodeView{
		Node:        n,
		Perms:       perm.Codes(),
		PermValue:   uint32(perm),
		SizeText:    HumanSize(n.Size),
		CreatorName: creator,
		Editable:    !n.IsDir && IsOfficeDocument(n.Name),
		Previewable: !n.IsDir && IsBrowserPreviewable(n.MimeType, n.Ext),
		IsPDF:       !n.IsDir && IsPDFLike(n.Name),
	}
}

func (s *FileService) creatorNames(items []model.Node) (map[uint64]string, error) {
	ids := make([]uint64, 0, len(items))
	seen := map[uint64]struct{}{}
	for _, n := range items {
		if n.CreatedBy == 0 {
			continue
		}
		if _, ok := seen[n.CreatedBy]; ok {
			continue
		}
		seen[n.CreatedBy] = struct{}{}
		ids = append(ids, n.CreatedBy)
	}
	if len(ids) == 0 {
		return map[uint64]string{}, nil
	}
	var users []model.User
	if err := s.db.Select("id", "username", "nickname").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, fmt.Errorf("查询创建人失败: %w", err)
	}
	out := make(map[uint64]string, len(users))
	for _, u := range users {
		name := u.Nickname
		if name == "" {
			name = u.Username
		}
		out[u.ID] = name
	}
	return out, nil
}

// Crumbs 返回从空间根到该节点的面包屑。
func (s *FileService) Crumbs(node *model.Node) ([]Crumb, error) {
	crumbs := []Crumb{{ID: 0, Name: "根目录"}}
	if node == nil {
		return crumbs, nil
	}
	ids := treex.IDs(node.Path)
	if len(ids) == 0 {
		return crumbs, nil
	}
	var nodes []model.Node
	if err := s.db.Select("id", "name", "path").Where("id IN ?", ids).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("查询路径失败: %w", err)
	}
	byID := make(map[uint64]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			crumbs = append(crumbs, Crumb{ID: n.ID, Name: n.Name})
		}
	}
	return crumbs, nil
}

// Mkdir 在指定目录下新建子目录。
func (s *FileService) Mkdir(subj *Subject, spaceID, parentID uint64, name string) (*model.Node, error) {
	name = sanitizeName(name)
	if name == "" {
		return nil, response.BadRequest("目录名不能为空")
	}
	space, err := s.space.Get(spaceID)
	if err != nil {
		return nil, err
	}
	parent, err := s.GetNode(spaceID, parentID)
	if err != nil {
		return nil, err
	}
	if parent != nil && !parent.IsDir {
		return nil, response.BadRequest("目标不是目录")
	}
	if _, err := s.acl.Require(subj, space, parent, model.PermUpload); err != nil {
		return nil, err
	}
	if exists, err := s.nameExists(spaceID, parentID, name, 0); err != nil {
		return nil, err
	} else if exists {
		return nil, response.Conflict("同名目录或文件已存在")
	}

	parentPath := treex.Root
	depth := 0
	if parent != nil {
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	node := &model.Node{
		SpaceID:   spaceID,
		ParentID:  parentID,
		Name:      name,
		IsDir:     true,
		Depth:     depth,
		CreatedBy: subj.User.ID,
		UpdatedBy: subj.User.ID,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(node).Error; err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		node.Path = treex.Build(parentPath, node.ID)
		return tx.Model(node).Update("path", node.Path).Error
	})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// CreateFile 在目录下登记一个文件节点（内容已经落盘，这里只写元数据）。
//
// 同名文件会自动加序号而不是报错——批量上传时弹一堆"已存在"最让人恼火。
func (s *FileService) CreateFile(tx *gorm.DB, subj *Subject, space *model.Space, parent *model.Node, name, blobHash string, size int64) (*model.Node, error) {
	name = sanitizeName(name)
	if name == "" {
		return nil, response.BadRequest("文件名不能为空")
	}
	var parentID uint64
	parentPath := treex.Root
	depth := 0
	if parent != nil {
		parentID = parent.ID
		parentPath = parent.Path
		depth = parent.Depth + 1
	}

	finalName, err := s.uniqueName(tx, space.ID, parentID, name)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(finalName), "."))
	node := &model.Node{
		SpaceID:   space.ID,
		ParentID:  parentID,
		Name:      finalName,
		IsDir:     false,
		Depth:     depth,
		Size:      size,
		BlobHash:  blobHash,
		MimeType:  guessMime(ext),
		Ext:       ext,
		Version:   1,
		CreatedBy: subj.User.ID,
		UpdatedBy: subj.User.ID,
	}
	if err := tx.Create(node).Error; err != nil {
		return nil, fmt.Errorf("创建文件失败: %w", err)
	}
	node.Path = treex.Build(parentPath, node.ID)
	if err := tx.Model(node).Update("path", node.Path).Error; err != nil {
		return nil, fmt.Errorf("回写文件路径失败: %w", err)
	}
	if err := s.retainBlob(tx, blobHash, size); err != nil {
		return nil, err
	}
	if err := s.space.AddUsage(tx, space.ID, size); err != nil {
		return nil, err
	}
	return node, nil
}

// retainBlob 给内容加一次引用；内容尚未登记时顺便建档。
func (s *FileService) retainBlob(tx *gorm.DB, hash string, size int64) error {
	if hash == "" {
		return nil
	}
	var blob model.Blob
	err := tx.Where("hash = ?", hash).First(&blob).Error
	switch {
	case err == nil:
		if err := tx.Model(&model.Blob{}).Where("hash = ?", hash).
			UpdateColumn("ref_count", gorm.Expr("ref_count + 1")).Error; err != nil {
			return fmt.Errorf("更新内容引用失败: %w", err)
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		storePath, err := s.store.Path(hash)
		if err != nil {
			return err
		}
		blob = model.Blob{Hash: hash, Size: size, RefCount: 1, StorePath: storePath}
		if err := tx.Create(&blob).Error; err != nil {
			return fmt.Errorf("登记内容失败: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("查询内容失败: %w", err)
	}
}

// releaseBlob 释放一次引用，归零时把磁盘上的内容也删掉。
func (s *FileService) releaseBlob(tx *gorm.DB, hash string) error {
	if hash == "" {
		return nil
	}
	if err := tx.Model(&model.Blob{}).Where("hash = ?", hash).
		UpdateColumn("ref_count", gorm.Expr("CASE WHEN ref_count > 0 THEN ref_count - 1 ELSE 0 END")).Error; err != nil {
		return fmt.Errorf("释放内容引用失败: %w", err)
	}
	var blob model.Blob
	if err := tx.Where("hash = ?", hash).First(&blob).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("查询内容失败: %w", err)
	}
	if blob.RefCount > 0 {
		return nil
	}
	if err := tx.Delete(&model.Blob{}, "hash = ?", hash).Error; err != nil {
		return fmt.Errorf("删除内容记录失败: %w", err)
	}
	return s.store.Remove(hash)
}

// FindByHash 按内容哈希查找已存在的文件，用于秒传。
func (s *FileService) FindByHash(hash string) (*model.Blob, error) {
	var blob model.Blob
	err := s.db.Where("hash = ?", hash).First(&blob).Error
	switch {
	case err == nil:
		if !s.store.Exists(hash) {
			// 数据库有记录但磁盘文件没了（人工删过目录），当作未命中重传。
			return nil, nil
		}
		return &blob, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil
	default:
		return nil, fmt.Errorf("查询内容失败: %w", err)
	}
}

// Rename 重命名节点。
func (s *FileService) Rename(subj *Subject, spaceID, nodeID uint64, newName string) (*model.Node, error) {
	newName = sanitizeName(newName)
	if newName == "" {
		return nil, response.BadRequest("名称不能为空")
	}
	space, err := s.space.Get(spaceID)
	if err != nil {
		return nil, err
	}
	node, err := s.GetNode(spaceID, nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, response.BadRequest("不能重命名根目录")
	}
	if _, err := s.acl.Require(subj, space, node, model.PermEdit); err != nil {
		return nil, err
	}
	if exists, err := s.nameExists(spaceID, node.ParentID, newName, node.ID); err != nil {
		return nil, err
	} else if exists {
		return nil, response.Conflict("同名目录或文件已存在")
	}

	updates := map[string]any{"name": newName, "updated_by": subj.User.ID}
	if !node.IsDir {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(newName), "."))
		updates["ext"] = ext
		updates["mime_type"] = guessMime(ext)
	}
	if err := s.db.Model(&model.Node{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("重命名失败: %w", err)
	}
	return s.GetNode(spaceID, nodeID)
}

// MoveInput 是移动/复制的入参。
type MoveInput struct {
	SpaceID       uint64   `json:"space_id"`
	NodeIDs       []uint64 `json:"node_ids"`
	TargetSpaceID uint64   `json:"target_space_id"`
	TargetID      uint64   `json:"target_id"`
}

// Move 把一批节点移动到目标目录（支持跨空间移动）。
func (s *FileService) Move(subj *Subject, in MoveInput) (int, error) {
	srcSpace, dstSpace, dstParent, err := s.prepareTransfer(subj, in)
	if err != nil {
		return 0, err
	}

	// 先把节点全部取出来、权限全部校验完，再进事务。
	// 事务里只能用 tx 发查询，混用连接池会在 SQLite 单连接下死锁。
	nodes := make([]*model.Node, 0, len(in.NodeIDs))
	for _, id := range in.NodeIDs {
		node, err := s.GetNode(in.SpaceID, id)
		if err != nil {
			return 0, err
		}
		if node == nil {
			continue
		}
		// 移动要同时具备来源的删除权和目标的上传权，否则等于绕过权限搬运数据。
		if _, err := s.acl.Require(subj, srcSpace, node, model.PermDelete); err != nil {
			return 0, err
		}
		if dstParent != nil && treex.IsDescendant(dstParent.Path, node.Path) {
			return 0, response.BadRequest("不能把目录移动到它自己的子目录里")
		}
		nodes = append(nodes, node)
	}

	moved := 0
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for _, node := range nodes {
			if err := s.moveOne(tx, subj, node, srcSpace, dstSpace, dstParent); err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}

func (s *FileService) prepareTransfer(subj *Subject, in MoveInput) (*model.Space, *model.Space, *model.Node, error) {
	if len(in.NodeIDs) == 0 {
		return nil, nil, nil, response.BadRequest("请选择要操作的文件")
	}
	srcSpace, err := s.space.Get(in.SpaceID)
	if err != nil {
		return nil, nil, nil, err
	}
	targetSpaceID := in.TargetSpaceID
	if targetSpaceID == 0 {
		targetSpaceID = in.SpaceID
	}
	dstSpace, err := s.space.Get(targetSpaceID)
	if err != nil {
		return nil, nil, nil, err
	}
	dstParent, err := s.GetNode(dstSpace.ID, in.TargetID)
	if err != nil {
		return nil, nil, nil, err
	}
	if dstParent != nil && !dstParent.IsDir {
		return nil, nil, nil, response.BadRequest("目标不是目录")
	}
	if _, err := s.acl.Require(subj, dstSpace, dstParent, model.PermUpload); err != nil {
		return nil, nil, nil, err
	}
	return srcSpace, dstSpace, dstParent, nil
}

func (s *FileService) moveOne(tx *gorm.DB, subj *Subject, node *model.Node, srcSpace, dstSpace *model.Space, dstParent *model.Node) error {
	var dstParentID uint64
	dstParentPath := treex.Root
	dstDepth := 0
	if dstParent != nil {
		dstParentID = dstParent.ID
		dstParentPath = dstParent.Path
		dstDepth = dstParent.Depth + 1
	}
	if node.ParentID == dstParentID && node.SpaceID == dstSpace.ID {
		return nil
	}

	name, err := s.uniqueName(tx, dstSpace.ID, dstParentID, node.Name)
	if err != nil {
		return err
	}
	oldPath := node.Path
	newPath := treex.Build(dstParentPath, node.ID)
	depthDelta := dstDepth - node.Depth

	subtreeSize, err := s.subtreeSize(tx, node)
	if err != nil {
		return err
	}
	if node.SpaceID != dstSpace.ID {
		if err := s.space.CheckQuota(dstSpace, subtreeSize); err != nil {
			return err
		}
	}

	updates := map[string]any{
		"parent_id":  dstParentID,
		"path":       newPath,
		"depth":      dstDepth,
		"name":       name,
		"space_id":   dstSpace.ID,
		"updated_by": subj.User.ID,
	}
	if err := tx.Model(&model.Node{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("移动失败: %w", err)
	}

	if node.IsDir {
		var children []model.Node
		if err := tx.Where("path LIKE ? AND id <> ?", treex.LikePrefix(strings.TrimSuffix(oldPath, "/")), node.ID).
			Find(&children).Error; err != nil {
			return fmt.Errorf("查询子节点失败: %w", err)
		}
		for _, child := range children {
			err := tx.Model(&model.Node{}).Where("id = ?", child.ID).Updates(map[string]any{
				"path":     treex.Rebase(child.Path, oldPath, newPath),
				"depth":    child.Depth + depthDelta,
				"space_id": dstSpace.ID,
			}).Error
			if err != nil {
				return fmt.Errorf("更新子节点路径失败: %w", err)
			}
		}
	}

	if node.SpaceID != dstSpace.ID && subtreeSize > 0 {
		if err := s.space.AddUsage(tx, srcSpace.ID, -subtreeSize); err != nil {
			return err
		}
		if err := s.space.AddUsage(tx, dstSpace.ID, subtreeSize); err != nil {
			return err
		}
	}
	return nil
}

// Copy 复制一批节点到目标目录。内容只加引用不重复落盘。
func (s *FileService) Copy(subj *Subject, in MoveInput) (int, error) {
	srcSpace, dstSpace, dstParent, err := s.prepareTransfer(subj, in)
	if err != nil {
		return 0, err
	}

	// 同 Move：校验与容量核算全部前置，事务内只做写入。
	nodes := make([]*model.Node, 0, len(in.NodeIDs))
	var totalSize int64
	for _, id := range in.NodeIDs {
		node, err := s.GetNode(in.SpaceID, id)
		if err != nil {
			return 0, err
		}
		if node == nil {
			continue
		}
		// 复制走的是"把内容带出去"，所以要求下载权。
		if _, err := s.acl.Require(subj, srcSpace, node, model.PermDownload); err != nil {
			return 0, err
		}
		if dstParent != nil && treex.IsDescendant(dstParent.Path, node.Path) {
			return 0, response.BadRequest("不能把目录复制到它自己的子目录里")
		}
		size, err := s.subtreeSize(s.db, node)
		if err != nil {
			return 0, err
		}
		totalSize += size
		nodes = append(nodes, node)
	}
	if err := s.space.CheckQuota(dstSpace, totalSize); err != nil {
		return 0, err
	}

	copied := 0
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for _, node := range nodes {
			if _, err := s.copyOne(tx, subj, node, dstSpace, dstParent); err != nil {
				return err
			}
			copied++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return copied, nil
}

func (s *FileService) copyOne(tx *gorm.DB, subj *Subject, node *model.Node, dstSpace *model.Space, dstParent *model.Node) (*model.Node, error) {
	var dstParentID uint64
	dstParentPath := treex.Root
	dstDepth := 0
	if dstParent != nil {
		dstParentID = dstParent.ID
		dstParentPath = dstParent.Path
		dstDepth = dstParent.Depth + 1
	}
	name, err := s.uniqueName(tx, dstSpace.ID, dstParentID, node.Name)
	if err != nil {
		return nil, err
	}

	clone := &model.Node{
		SpaceID:   dstSpace.ID,
		ParentID:  dstParentID,
		Name:      name,
		IsDir:     node.IsDir,
		Depth:     dstDepth,
		Size:      node.Size,
		BlobHash:  node.BlobHash,
		MimeType:  node.MimeType,
		Ext:       node.Ext,
		Version:   1,
		CreatedBy: subj.User.ID,
		UpdatedBy: subj.User.ID,
	}
	if err := tx.Create(clone).Error; err != nil {
		return nil, fmt.Errorf("复制失败: %w", err)
	}
	clone.Path = treex.Build(dstParentPath, clone.ID)
	if err := tx.Model(clone).Update("path", clone.Path).Error; err != nil {
		return nil, fmt.Errorf("回写复制节点路径失败: %w", err)
	}
	if !node.IsDir {
		if err := s.retainBlob(tx, node.BlobHash, node.Size); err != nil {
			return nil, err
		}
		if err := s.space.AddUsage(tx, dstSpace.ID, node.Size); err != nil {
			return nil, err
		}
		return clone, nil
	}

	var children []model.Node
	if err := tx.Where("space_id = ? AND parent_id = ? AND trashed = ?", node.SpaceID, node.ID, false).
		Find(&children).Error; err != nil {
		return nil, fmt.Errorf("查询子节点失败: %w", err)
	}
	for i := range children {
		if _, err := s.copyOne(tx, subj, &children[i], dstSpace, clone); err != nil {
			return nil, err
		}
	}
	return clone, nil
}

// Trash 把一批节点移入回收站（连同其子树）。
func (s *FileService) Trash(subj *Subject, spaceID uint64, nodeIDs []uint64) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, response.BadRequest("请选择要删除的文件")
	}
	space, err := s.space.Get(spaceID)
	if err != nil {
		return 0, err
	}

	// 校验前置，事务内只写。
	targets := make([]*model.Node, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node, err := s.GetNode(spaceID, id)
		if err != nil {
			return 0, err
		}
		if node == nil || node.Trashed {
			continue
		}
		if _, err := s.acl.Require(subj, space, node, model.PermDelete); err != nil {
			return 0, err
		}
		targets = append(targets, node)
	}

	now := time.Now()
	count := 0
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for _, node := range targets {
			updates := map[string]any{
				"trashed":         true,
				"trash_root_id":   node.ID,
				"trashed_at":      now,
				"trashed_by":      subj.User.ID,
				"trash_parent_id": node.ParentID,
			}
			if err := tx.Model(&model.Node{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
				return fmt.Errorf("删除失败: %w", err)
			}
			if node.IsDir {
				err := tx.Model(&model.Node{}).
					Where("space_id = ? AND path LIKE ? AND id <> ?", spaceID, treex.LikePrefix(strings.TrimSuffix(node.Path, "/")), node.ID).
					Updates(map[string]any{
						"trashed":       true,
						"trash_root_id": node.ID,
						"trashed_at":    now,
						"trashed_by":    subj.User.ID,
					}).Error
				if err != nil {
					return fmt.Errorf("删除子节点失败: %w", err)
				}
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// TrashItem 是回收站列表项。
type TrashItem struct {
	model.Node
	SizeText   string `json:"size_text"`
	SpaceName  string `json:"space_name"`
	DeletedBy  string `json:"deleted_by_name"`
	OriginPath string `json:"origin_path"`
}

// ListTrash 列出回收站条目，只返回每次删除的顶层节点。
func (s *FileService) ListTrash(subj *Subject, spaceID uint64, page, pageSize int) ([]TrashItem, int64, error) {
	tx := s.db.Model(&model.Node{}).Where("trashed = ?", true).
		Where("trash_root_id = id")
	if spaceID > 0 {
		if _, err := s.space.Get(spaceID); err != nil {
			return nil, 0, err
		}
		tx = tx.Where("space_id = ?", spaceID)
	} else if !subj.IsSuperAdmin() {
		views, err := s.space.VisibleSpaces(subj)
		if err != nil {
			return nil, 0, err
		}
		ids := make([]uint64, 0, len(views))
		for _, v := range views {
			ids = append(ids, v.ID)
		}
		if len(ids) == 0 {
			return []TrashItem{}, 0, nil
		}
		tx = tx.Where("space_id IN ?", ids)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计回收站失败: %w", err)
	}
	page, size := normalizePage(page, pageSize)
	var nodes []model.Node
	err := tx.Order("trashed_at desc").Offset((page - 1) * size).Limit(size).Find(&nodes).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询回收站失败: %w", err)
	}

	// 一页里通常只涉及少数几个空间，缓存一下免得每条都回库查一次。
	spaceCache := map[uint64]*model.Space{}
	items := make([]TrashItem, 0, len(nodes))
	for i := range nodes {
		n := nodes[i]
		sp, ok := spaceCache[n.SpaceID]
		if !ok {
			loaded, err := s.space.Get(n.SpaceID)
			if err != nil {
				continue
			}
			sp = loaded
			spaceCache[n.SpaceID] = sp
		}
		// 回收站里只保留有删除权的人能看到的条目。
		perm, err := s.acl.Effective(subj, sp, &n)
		if err != nil {
			return nil, 0, err
		}
		if !perm.Has(model.PermDelete) && n.TrashedBy != subj.User.ID {
			continue
		}
		items = append(items, TrashItem{
			Node:      n,
			SizeText:  HumanSize(n.Size),
			SpaceName: sp.Name,
		})
	}
	return items, total, nil
}

// Restore 从回收站还原一批节点。原位置已消失时退回空间根目录。
func (s *FileService) Restore(subj *Subject, nodeIDs []uint64) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, response.BadRequest("请选择要还原的条目")
	}
	// 校验前置：事务内不能再走连接池查询。
	targets, err := s.loadTrashTargets(subj, nodeIDs, true)
	if err != nil {
		return 0, err
	}

	count := 0
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for i := range targets {
			node := targets[i]

			parentID := node.TrashParentID
			parentPath := treex.Root
			depth := 0
			if parentID > 0 {
				var parent model.Node
				err := tx.Where("id = ? AND trashed = ?", parentID, false).First(&parent).Error
				if err != nil {
					// 原目录不在了，退回根目录，总比还原失败强。
					parentID = 0
				} else {
					parentPath = parent.Path
					depth = parent.Depth + 1
				}
			}
			name, err := s.uniqueName(tx, node.SpaceID, parentID, node.Name)
			if err != nil {
				return err
			}
			oldPath := node.Path
			newPath := treex.Build(parentPath, node.ID)
			depthDelta := depth - node.Depth

			err = tx.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]any{
				"trashed": false, "trash_root_id": 0, "trashed_at": nil, "trashed_by": 0,
				"trash_parent_id": 0, "parent_id": parentID, "path": newPath, "depth": depth, "name": name,
			}).Error
			if err != nil {
				return fmt.Errorf("还原失败: %w", err)
			}

			var children []model.Node
			if err := tx.Where("trash_root_id = ? AND id <> ?", node.ID, node.ID).Find(&children).Error; err != nil {
				return fmt.Errorf("查询回收站子节点失败: %w", err)
			}
			for _, child := range children {
				err := tx.Model(&model.Node{}).Where("id = ?", child.ID).Updates(map[string]any{
					"trashed": false, "trash_root_id": 0, "trashed_at": nil, "trashed_by": 0,
					"path": treex.Rebase(child.Path, oldPath, newPath), "depth": child.Depth + depthDelta,
				}).Error
				if err != nil {
					return fmt.Errorf("还原子节点失败: %w", err)
				}
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Purge 彻底删除回收站中的条目。nodeIDs 为空表示清空当前可见的回收站。
func (s *FileService) Purge(subj *Subject, nodeIDs []uint64) (int, error) {
	if len(nodeIDs) == 0 {
		items, _, err := s.ListTrash(subj, 0, 1, 200)
		if err != nil {
			return 0, err
		}
		for _, item := range items {
			nodeIDs = append(nodeIDs, item.ID)
		}
	}
	// 校验前置：事务内不能再走连接池查询。
	targets, err := s.loadTrashTargets(subj, nodeIDs, false)
	if err != nil {
		return 0, err
	}

	count := 0
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for i := range targets {
			if err := s.purgeSubtree(tx, targets[i]); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// loadTrashTargets 读取回收站条目并逐个校验删除权限。
//
// rootOnly 为 true 时只接受"整次删除的顶层节点"（还原必须成组进行，
// 单独还原一个子文件会让它脱离原来的目录结构）。
func (s *FileService) loadTrashTargets(subj *Subject, nodeIDs []uint64, rootOnly bool) ([]*model.Node, error) {
	spaceCache := map[uint64]*model.Space{}
	out := make([]*model.Node, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		var node model.Node
		if err := s.db.First(&node, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, fmt.Errorf("加载回收站条目失败: %w", err)
		}
		if !node.Trashed {
			return nil, response.BadRequest("只能操作回收站中的条目")
		}
		if rootOnly && node.TrashRootID != node.ID {
			continue
		}
		space, ok := spaceCache[node.SpaceID]
		if !ok {
			loaded, err := s.space.Get(node.SpaceID)
			if err != nil {
				return nil, err
			}
			space = loaded
			spaceCache[node.SpaceID] = space
		}
		if _, err := s.acl.Require(subj, space, &node, model.PermDelete); err != nil {
			return nil, err
		}
		item := node
		out = append(out, &item)
	}
	return out, nil
}

func (s *FileService) purgeSubtree(tx *gorm.DB, root *model.Node) error {
	targets := []model.Node{*root}
	if root.IsDir {
		var children []model.Node
		if err := tx.Where("path LIKE ? AND id <> ?", treex.LikePrefix(strings.TrimSuffix(root.Path, "/")), root.ID).
			Find(&children).Error; err != nil {
			return fmt.Errorf("查询子节点失败: %w", err)
		}
		targets = append(targets, children...)
	}

	var freed int64
	ids := make([]uint64, 0, len(targets))
	stones := make([]model.NodeTombstone, 0, len(targets))
	for _, n := range targets {
		ids = append(ids, n.ID)
		// 这里是系统里唯一真正删除节点行的地方，所以墓碑只需要在这里写。
		// 不留墓碑的话，外部索引程序靠 updated_at 游标永远发现不了这些文件没了。
		stones = append(stones, model.NodeTombstone{
			NodeID: n.ID, SpaceID: n.SpaceID, BlobHash: n.BlobHash,
			Name: n.Name, Path: n.Path, IsDir: n.IsDir, DeletedBy: n.TrashedBy,
		})
		if !n.IsDir {
			freed += n.Size
			if err := s.releaseBlob(tx, n.BlobHash); err != nil {
				return err
			}
		}
	}
	if err := tx.Create(&stones).Error; err != nil {
		return fmt.Errorf("记录删除流水失败: %w", err)
	}
	if err := tx.Where("node_id IN ?", ids).Delete(&model.AccessRule{}).Error; err != nil {
		return fmt.Errorf("清理节点权限失败: %w", err)
	}
	if err := tx.Where("node_id IN ?", ids).Delete(&model.Share{}).Error; err != nil {
		return fmt.Errorf("清理节点分享失败: %w", err)
	}
	if err := tx.Where("id IN ?", ids).Delete(&model.Node{}).Error; err != nil {
		return fmt.Errorf("彻底删除失败: %w", err)
	}
	return s.space.AddUsage(tx, root.SpaceID, -freed)
}

// UpdateContent 用新的内容覆盖一个已存在的文件（Office 在线编辑保存走这条路）。
func (s *FileService) UpdateContent(userID uint64, nodeID uint64, blobHash string, size int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var node model.Node
		if err := tx.First(&node, nodeID).Error; err != nil {
			return fmt.Errorf("加载文件失败: %w", err)
		}
		if node.IsDir {
			return response.BadRequest("目录不能保存内容")
		}
		if node.BlobHash == blobHash {
			return nil
		}
		oldHash := node.BlobHash
		oldSize := node.Size
		err := tx.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]any{
			"blob_hash":  blobHash,
			"size":       size,
			"version":    gorm.Expr("version + 1"),
			"updated_by": userID,
		}).Error
		if err != nil {
			return fmt.Errorf("更新文件内容失败: %w", err)
		}
		if err := s.retainBlob(tx, blobHash, size); err != nil {
			return err
		}
		if err := s.releaseBlob(tx, oldHash); err != nil {
			return err
		}
		return s.space.AddUsage(tx, node.SpaceID, size-oldSize)
	})
}

// nameExists 判断同级下是否已有同名条目。
func (s *FileService) nameExists(spaceID, parentID uint64, name string, excludeID uint64) (bool, error) {
	tx := s.db.Model(&model.Node{}).
		Where("space_id = ? AND parent_id = ? AND name = ? AND trashed = ?", spaceID, parentID, name, false)
	if excludeID > 0 {
		tx = tx.Where("id <> ?", excludeID)
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("检查重名失败: %w", err)
	}
	return count > 0, nil
}

// uniqueName 生成同级下不冲突的名字，例如 "方案.docx" -> "方案(1).docx"。
func (s *FileService) uniqueName(tx *gorm.DB, spaceID, parentID uint64, name string) (string, error) {
	base := name
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		var count int64
		err := tx.Model(&model.Node{}).
			Where("space_id = ? AND parent_id = ? AND name = ? AND trashed = ?", spaceID, parentID, base, false).
			Count(&count).Error
		if err != nil {
			return "", fmt.Errorf("检查重名失败: %w", err)
		}
		if count == 0 {
			return base, nil
		}
		base = fmt.Sprintf("%s(%d)%s", stem, i+1, ext)
	}
	return "", response.Conflict("同名文件过多，请重命名后再试")
}

// subtreeSize 统计节点（含子树）占用的字节数。
func (s *FileService) subtreeSize(tx *gorm.DB, node *model.Node) (int64, error) {
	if !node.IsDir {
		return node.Size, nil
	}
	var total *int64
	err := tx.Model(&model.Node{}).
		Where("space_id = ? AND path LIKE ? AND is_dir = ?", node.SpaceID, treex.LikePrefix(strings.TrimSuffix(node.Path, "/")), false).
		Select("COALESCE(SUM(size), 0)").Scan(&total).Error
	if err != nil {
		return 0, fmt.Errorf("统计目录大小失败: %w", err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// SubtreeFile 是打包下载时的一个条目，RelPath 是 zip 包内的相对路径。
type SubtreeFile struct {
	model.Node
	RelPath string
}

// SubtreeFiles 列出目录下的全部文件（含各级子目录），并算好 zip 内的相对路径。
func (s *FileService) SubtreeFiles(root *model.Node) ([]SubtreeFile, error) {
	if !root.IsDir {
		return []SubtreeFile{{Node: *root, RelPath: root.Name}}, nil
	}
	var nodes []model.Node
	err := s.db.Where("space_id = ? AND path LIKE ? AND trashed = ?",
		root.SpaceID, treex.LikePrefix(strings.TrimSuffix(root.Path, "/")), false).
		Order("depth asc, name asc").Limit(20000).Find(&nodes).Error
	if err != nil {
		return nil, fmt.Errorf("查询目录内容失败: %w", err)
	}

	// 先把每个节点的名字记下来，再靠物化路径拼出相对路径，避免逐层回查。
	names := map[uint64]string{root.ID: ""}
	for _, n := range nodes {
		names[n.ID] = n.Name
	}
	out := make([]SubtreeFile, 0, len(nodes))
	for _, n := range nodes {
		if n.IsDir {
			continue
		}
		ids := treex.IDs(n.Path)
		parts := make([]string, 0, len(ids))
		collecting := false
		for _, id := range ids {
			if id == root.ID {
				collecting = true
				continue
			}
			if !collecting {
				continue
			}
			if name, ok := names[id]; ok && name != "" {
				parts = append(parts, name)
			}
		}
		if len(parts) == 0 {
			parts = append(parts, n.Name)
		}
		out = append(out, SubtreeFile{Node: n, RelPath: strings.Join(parts, "/")})
	}
	return out, nil
}

// OpenContent 打开文件内容，供下载与预览使用。
func (s *FileService) OpenContent(node *model.Node) (*os.File, error) {
	if node.IsDir {
		return nil, response.BadRequest("目录不能下载")
	}
	if node.BlobHash == "" {
		return nil, response.NotFound("文件内容不存在")
	}
	return s.store.Open(node.BlobHash)
}

// sanitizeName 清洗文件名：去掉路径分隔符与控制字符，避免穿越目录。
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, ".")
	if len([]rune(name)) > 200 {
		ext := path.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		r := []rune(stem)
		if len(r) > 180 {
			r = r[:180]
		}
		name = string(r) + ext
	}
	return name
}

var extraMimeTypes = map[string]string{
	"md": "text/markdown; charset=utf-8", "txt": "text/plain; charset=utf-8",
	"log": "text/plain; charset=utf-8", "json": "application/json; charset=utf-8",
	"yaml": "text/yaml; charset=utf-8", "yml": "text/yaml; charset=utf-8",
	"doc": "application/msword", "xls": "application/vnd.ms-excel", "ppt": "application/vnd.ms-powerpoint",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"odt":  "application/vnd.oasis.opendocument.text",
	"ods":  "application/vnd.oasis.opendocument.spreadsheet",
	"odp":  "application/vnd.oasis.opendocument.presentation",
}

func guessMime(ext string) string {
	if ext == "" {
		return "application/octet-stream"
	}
	if m, ok := extraMimeTypes[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension("." + ext); m != "" {
		return m
	}
	return "application/octet-stream"
}

var browserPreviewExts = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "bmp": true, "svg": true,
	"mp4": true, "webm": true, "ogg": true, "mp3": true, "wav": true, "m4a": true,
	"pdf": true, "txt": true, "md": true, "log": true, "json": true, "xml": true,
	"yaml": true, "yml": true, "csv": true, "html": true, "css": true, "js": true, "ts": true,
	"go": true, "java": true, "py": true, "c": true, "cpp": true, "h": true, "sh": true, "sql": true,
}

// IsBrowserPreviewable 判断浏览器能否直接预览该文件。
func IsBrowserPreviewable(mimeType, ext string) bool {
	if browserPreviewExts[strings.ToLower(ext)] {
		return true
	}
	return strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "video/") ||
		strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "text/")
}
