package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// AIService 面向外部 AI Agent / 索引程序的只读数据接口。
//
// 这一层刻意不做任何"帮你想好的"聚合：只负责把文档元数据、变更、原始内容、
// 以及"某个人能看到哪些文档"如实交出去，具体怎么切块、怎么向量化由调用方决定。
type AIService struct {
	db    *gorm.DB
	acl   *ACLService
	file  *FileService
	space *SpaceService
	user  *UserService
}

// NewAIService 构造服务。
func NewAIService(db *gorm.DB, acl *ACLService, file *FileService, space *SpaceService, user *UserService) *AIService {
	return &AIService{db: db, acl: acl, file: file, space: space, user: user}
}

// DocumentItem 是交给索引程序的一条文档记录。
type DocumentItem struct {
	NodeID   uint64 `json:"node_id"`
	SpaceID  uint64 `json:"space_id"`
	ParentID uint64 `json:"parent_id"`
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	// Path 是 ID 形式的物化路径，配合 PathNames 能还原出人类可读的位置。
	Path      string   `json:"path"`
	PathNames []string `json:"path_names"`
	Size      int64    `json:"size"`
	// BlobHash 是内容指纹。乐云是内容寻址存储，同一份文件被多个部门各存一份时
	// 哈希相同——索引程序按它去重，可以只解析一次。
	BlobHash  string    `json:"blob_hash,omitempty"`
	MimeType  string    `json:"mime_type,omitempty"`
	Ext       string    `json:"ext,omitempty"`
	Version   int       `json:"version"`
	Trashed   bool      `json:"trashed"`
	CreatedBy uint64    `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// DocumentPage 是一页枚举结果。
type DocumentPage struct {
	Items []DocumentItem `json:"items"`
	// NextCursor 为空表示已经拉完。把它原样带回即可继续。
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// DocumentQuery 是枚举条件。
type DocumentQuery struct {
	// Cursor 由上一页返回，内部编码了 (updated_at, id)。
	Cursor string
	Limit  int
	// SpaceIDs 为空表示不限（仍受密钥的空间白名单约束）。
	SpaceIDs []uint64
	// IncludeTrashed 为 true 时连回收站里的条目一起返回，
	// 便于索引程序把它们标记为暂不可用而不是直接丢弃。
	IncludeTrashed bool
	// IncludeDirs 为 true 时把目录也返回，默认只给文件。
	IncludeDirs bool
	// UpdatedAfter 额外的时间下界，用于"只要最近一周的"这类场景。
	UpdatedAfter *time.Time
}

// 游标编码成不透明字符串：调用方不该依赖它的内部结构，将来换实现才不会破坏兼容。
func encodeCursor(t time.Time, id uint64) string {
	raw := fmt.Sprintf("%d:%d", t.UTC().UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, uint64, error) {
	if cursor == "" {
		return time.Time{}, 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, response.BadRequest("游标格式不正确")
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, response.BadRequest("游标格式不正确")
	}
	nanos, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, response.BadRequest("游标格式不正确")
	}
	return time.Unix(0, nanos).UTC(), id, nil
}

// ListDocuments 按 (updated_at, id) 复合游标枚举文档。
//
// 用复合游标而不是 offset 分页：同步过程中随时会有文件被改动，
// offset 会让记录在翻页之间挪位，漏掉或重复。
// 同一毫秒内的多条记录靠 id 兜底排序，保证全序。
func (s *AIService) ListDocuments(q DocumentQuery) (*DocumentPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	cursorTime, cursorID, err := decodeCursor(q.Cursor)
	if err != nil {
		return nil, err
	}

	tx := s.db.Model(&model.Node{})
	if !q.IncludeDirs {
		tx = tx.Where("is_dir = ?", false)
	}
	if !q.IncludeTrashed {
		tx = tx.Where("trashed = ?", false)
	}
	if len(q.SpaceIDs) > 0 {
		tx = tx.Where("space_id IN ?", q.SpaceIDs)
	}
	if q.UpdatedAfter != nil {
		tx = tx.Where("updated_at > ?", *q.UpdatedAfter)
	}
	if q.Cursor != "" {
		tx = tx.Where("updated_at > ? OR (updated_at = ? AND id > ?)", cursorTime, cursorTime, cursorID)
	}

	var rows []model.Node
	// 多取一条用来判断还有没有下一页，省掉一次 count。
	if err := tx.Order("updated_at asc, id asc").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("枚举文档失败: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	names, err := s.pathNames(rows)
	if err != nil {
		return nil, err
	}

	page := &DocumentPage{Items: make([]DocumentItem, 0, len(rows)), HasMore: hasMore}
	for _, n := range rows {
		page.Items = append(page.Items, DocumentItem{
			NodeID: n.ID, SpaceID: n.SpaceID, ParentID: n.ParentID,
			Name: n.Name, IsDir: n.IsDir, Path: n.Path,
			PathNames: names[n.ID], Size: n.Size, BlobHash: n.BlobHash,
			MimeType: n.MimeType, Ext: n.Ext, Version: n.Version,
			Trashed: n.Trashed, CreatedBy: n.CreatedBy,
			UpdatedAt: n.UpdatedAt, CreatedAt: n.CreatedAt,
		})
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor = encodeCursor(last.UpdatedAt, last.ID)
	} else {
		page.NextCursor = q.Cursor
	}
	return page, nil
}

// pathNames 把物化路径里的 ID 翻成目录名，让索引程序能拼出可读的文件位置。
func (s *AIService) pathNames(rows []model.Node) (map[uint64][]string, error) {
	idSet := map[uint64]struct{}{}
	for _, n := range rows {
		for _, id := range treex.IDs(n.Path) {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return map[uint64][]string{}, nil
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var named []model.Node
	if err := s.db.Select("id", "name").Where("id IN ?", ids).Find(&named).Error; err != nil {
		return nil, fmt.Errorf("查询路径名称失败: %w", err)
	}
	nameByID := make(map[uint64]string, len(named))
	for _, n := range named {
		nameByID[n.ID] = n.Name
	}

	out := make(map[uint64][]string, len(rows))
	for _, n := range rows {
		chain := treex.IDs(n.Path)
		parts := make([]string, 0, len(chain))
		for _, id := range chain {
			if name, ok := nameByID[id]; ok {
				parts = append(parts, name)
			}
		}
		out[n.ID] = parts
	}
	return out, nil
}

// DeletionPage 是一页删除流水。
type DeletionPage struct {
	Items      []model.NodeTombstone `json:"items"`
	NextCursor uint64                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
}

// ListDeletions 拉取彻底删除的流水。
//
// 为什么单独有这么一个接口：进回收站是软删除，会刷新 updated_at，
// 枚举游标扫得到；但彻底删除是真的把行删了，游标永远发现不了，
// 外部索引里就会留下指向已不存在文件的幽灵数据。
func (s *AIService) ListDeletions(cursor uint64, limit int, spaceIDs []uint64) (*DeletionPage, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	tx := s.db.Model(&model.NodeTombstone{}).Where("id > ?", cursor)
	if len(spaceIDs) > 0 {
		tx = tx.Where("space_id IN ?", spaceIDs)
	}
	var rows []model.NodeTombstone
	if err := tx.Order("id asc").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询删除流水失败: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	next := cursor
	if len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	return &DeletionPage{Items: rows, NextCursor: next, HasMore: hasMore}, nil
}

// AuthorizeResult 是批量鉴权的结果。
type AuthorizeResult struct {
	UserID uint64 `json:"user_id"`
	// Allowed 是该用户确实能看到的节点，顺序与请求无关。
	Allowed []uint64 `json:"allowed"`
	Denied  []uint64 `json:"denied"`
	// Missing 是数据库里已经不存在的节点，索引该把它们清掉了。
	Missing []uint64 `json:"missing"`
	// Perms 给出每个可见节点的具体权限，便于区分"能看"与"能下载"。
	Perms map[uint64][]string `json:"perms"`
}

// Authorize 批量判定某个用户能看到哪些节点。
//
// 这是"按提问人权限过滤"的落点：知识库检索回一批候选片段后，
// 必须先用它把不该看的剔掉，再把剩下的内容送进模型——
// 顺序反过来（先喂模型再叮嘱它别说）是拦不住泄露的。
func (s *AIService) Authorize(userID uint64, nodeIDs []uint64, want model.Permission) (*AuthorizeResult, error) {
	res := &AuthorizeResult{
		UserID:  userID,
		Allowed: []uint64{},
		Denied:  []uint64{},
		Missing: []uint64{},
		Perms:   map[uint64][]string{},
	}
	if len(nodeIDs) == 0 {
		return res, nil
	}
	if len(nodeIDs) > 1000 {
		return nil, response.BadRequest("单次最多判定 1000 个节点")
	}
	if want == model.PermNone {
		want = model.PermView
	}

	var user model.User
	if err := s.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("用户不存在")
		}
		return nil, fmt.Errorf("加载用户失败: %w", err)
	}
	// 停用的账号一律视为什么都看不到——离职交接期最容易在这里出事。
	if user.Status == model.UserDisabled {
		res.Denied = append(res.Denied, nodeIDs...)
		return res, nil
	}

	subj, err := s.acl.LoadSubject(&user)
	if err != nil {
		return nil, err
	}

	var nodes []model.Node
	if err := s.db.Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("加载节点失败: %w", err)
	}
	found := make(map[uint64]struct{}, len(nodes))
	for _, n := range nodes {
		found[n.ID] = struct{}{}
	}
	for _, id := range nodeIDs {
		if _, ok := found[id]; !ok {
			res.Missing = append(res.Missing, id)
		}
	}

	perms, err := s.acl.EffectiveBatch(subj, nodes)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		p := perms[n.ID]
		// 回收站里的条目对谁都不算"能看到"，哪怕权限够。
		if n.Trashed || !p.Has(want) {
			res.Denied = append(res.Denied, n.ID)
			continue
		}
		res.Allowed = append(res.Allowed, n.ID)
		res.Perms[n.ID] = p.Codes()
	}
	return res, nil
}

// SpaceBrief 是交给索引程序的空间概要。
type SpaceBrief struct {
	ID        uint64 `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	DeptID    uint64 `json:"dept_id,omitempty"`
	OwnerID   uint64 `json:"owner_id,omitempty"`
	Enabled   bool   `json:"enabled"`
	FileCount int64  `json:"file_count"`
	UsedBytes int64  `json:"used_bytes"`
}

// ListSpaces 列出空间概要，便于索引程序规划抓取范围。
func (s *AIService) ListSpaces(spaceIDs []uint64) ([]SpaceBrief, error) {
	tx := s.db.Model(&model.Space{})
	if len(spaceIDs) > 0 {
		tx = tx.Where("id IN ?", spaceIDs)
	}
	var spaces []model.Space
	if err := tx.Order("id asc").Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("查询空间失败: %w", err)
	}

	type countRow struct {
		SpaceID uint64
		Cnt     int64
	}
	var counts []countRow
	err := s.db.Model(&model.Node{}).
		Select("space_id as space_id, count(*) as cnt").
		Where("is_dir = ? AND trashed = ?", false, false).
		Group("space_id").Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("统计文件数失败: %w", err)
	}
	byID := make(map[uint64]int64, len(counts))
	for _, c := range counts {
		byID[c.SpaceID] = c.Cnt
	}

	out := make([]SpaceBrief, 0, len(spaces))
	for _, sp := range spaces {
		out = append(out, SpaceBrief{
			ID: sp.ID, Type: string(sp.Type), Name: sp.Name,
			DeptID: sp.DeptID, OwnerID: sp.OwnerID, Enabled: sp.Enabled,
			FileCount: byID[sp.ID], UsedBytes: sp.UsedBytes,
		})
	}
	return out, nil
}

// GetDocument 读取单个文档的元数据。
func (s *AIService) GetDocument(nodeID uint64) (*DocumentItem, *model.Node, error) {
	var n model.Node
	if err := s.db.First(&n, nodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, response.NotFound("文档不存在")
		}
		return nil, nil, fmt.Errorf("加载文档失败: %w", err)
	}
	names, err := s.pathNames([]model.Node{n})
	if err != nil {
		return nil, nil, err
	}
	return &DocumentItem{
		NodeID: n.ID, SpaceID: n.SpaceID, ParentID: n.ParentID,
		Name: n.Name, IsDir: n.IsDir, Path: n.Path, PathNames: names[n.ID],
		Size: n.Size, BlobHash: n.BlobHash, MimeType: n.MimeType, Ext: n.Ext,
		Version: n.Version, Trashed: n.Trashed, CreatedBy: n.CreatedBy,
		UpdatedAt: n.UpdatedAt, CreatedAt: n.CreatedAt,
	}, &n, nil
}

// FindNodeByBlob 找到引用某份内容的任意一个节点，用于按内容哈希取文件。
func (s *AIService) FindNodeByBlob(hash string) (*model.Node, error) {
	var n model.Node
	err := s.db.Where("blob_hash = ? AND is_dir = ?", hash, false).Order("id asc").First(&n).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("内容不存在")
		}
		return nil, fmt.Errorf("查询内容失败: %w", err)
	}
	return &n, nil
}

// UserBrief 是交给索引程序的用户概要。
type UserBrief struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	DeptID   uint64 `json:"dept_id"`
	DeptName string `json:"dept_name"`
	DeptPath string `json:"dept_path"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

// GetUser 读取用户概要，供 Agent 把 user_id 映射成人。
func (s *AIService) GetUser(userID uint64) (*UserBrief, error) {
	u, err := s.user.Get(userID)
	if err != nil {
		return nil, err
	}
	view, err := s.user.DecorateOne(u)
	if err != nil {
		return nil, err
	}
	return &UserBrief{
		ID: u.ID, Username: u.Username, Nickname: u.Nickname,
		DeptID: u.DeptID, DeptName: view.DeptName, DeptPath: view.DeptPath,
		Role: string(u.Role), Status: string(u.Status),
	}, nil
}

// ResolveUser 按用户名找人，Agent 那边往往只拿得到登录名。
func (s *AIService) ResolveUser(username string) (*UserBrief, error) {
	u, err := s.user.GetByUsername(strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	return s.GetUser(u.ID)
}
