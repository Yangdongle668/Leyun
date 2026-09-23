package service

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/storage"
)

// StatsService 汇总管理后台首页所需的统计数据。
type StatsService struct {
	db    *gorm.DB
	store *storage.Store
}

// NewStatsService 构造统计服务。
func NewStatsService(db *gorm.DB, store *storage.Store) *StatsService {
	return &StatsService{db: db, store: store}
}

// Overview 是概览统计。
type Overview struct {
	UserTotal     int64            `json:"user_total"`
	UserActive    int64            `json:"user_active"`
	DeptTotal     int64            `json:"dept_total"`
	SpaceTotal    int64            `json:"space_total"`
	FileTotal     int64            `json:"file_total"`
	FolderTotal   int64            `json:"folder_total"`
	TrashTotal    int64            `json:"trash_total"`
	ShareTotal    int64            `json:"share_total"`
	StoredBytes   int64            `json:"stored_bytes"`
	StoredText    string           `json:"stored_text"`
	LogicalBytes  int64            `json:"logical_bytes"`
	LogicalText   string           `json:"logical_text"`
	DedupSaved    int64            `json:"dedup_saved"`
	DedupText     string           `json:"dedup_saved_text"`
	TopSpaces     []SpaceUsage     `json:"top_spaces"`
	RecentUploads int64            `json:"recent_uploads"`
	DeptUsage     []DeptUsageBrief `json:"dept_usage"`
	// Scoped 为真表示这份数据只覆盖调用人管辖的那棵子树，不是全公司。
	// 界面据此标注口径，免得部门管理员把自己部门的数字当成全公司的。
	Scoped bool `json:"scoped"`
}

// SpaceUsage 是单个空间的用量。
type SpaceUsage struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Used     int64  `json:"used"`
	UsedText string `json:"used_text"`
	Quota    int64  `json:"quota"`
}

// DeptUsageBrief 是部门维度的用量。
type DeptUsageBrief struct {
	DeptID   uint64 `json:"dept_id"`
	Name     string `json:"name"`
	Members  int64  `json:"members"`
	Used     int64  `json:"used"`
	UsedText string `json:"used_text"`
}

// Overview 计算概览数据。
// Overview 计算概览数据。
//
// scopeDeptIDs 限定统计口径：传 nil 表示不限（超级管理员），
// 否则只统计这些部门及其下的内容。
//
// 不做这层限制的话，部门管理员打开概览页会看到全公司的人数、部门数、
// 空间用量——包括**别人的个人空间**。那是隐私，也不是他该管的范围。
func (s *StatsService) Overview(scopeDeptIDs []uint64) (*Overview, error) {
	out := &Overview{Scoped: scopeDeptIDs != nil}

	// 能统计哪些空间：管辖子树内的部门空间 + 公共空间。
	// 个人空间一律排除——员工的私人区域不进管理概览，哪怕是自己的：
	// 自己的用量在侧栏本来就看得到，放进"空间用量 Top"只会挤掉真正要看的。
	var scopeSpaceIDs []uint64
	if scopeDeptIDs != nil {
		if len(scopeDeptIDs) == 0 {
			return out, nil
		}
		err := s.db.Model(&model.Space{}).
			Where("(type = ? AND dept_id IN ?) OR type = ?",
				model.SpaceDepartment, scopeDeptIDs, model.SpacePublic).
			Pluck("id", &scopeSpaceIDs).Error
		if err != nil {
			return nil, fmt.Errorf("查询可统计空间失败: %w", err)
		}
		if len(scopeSpaceIDs) == 0 {
			// 一个空间都没有时给个不可能命中的值，避免 IN () 被当成无条件。
			scopeSpaceIDs = []uint64{0}
		}
	}

	// byDept / bySpace 给查询补上范围限制；不限范围时原样返回。
	byDept := func(tx *gorm.DB) *gorm.DB {
		if scopeDeptIDs == nil {
			return tx
		}
		return tx.Where("dept_id IN ?", scopeDeptIDs)
	}
	bySpace := func(tx *gorm.DB) *gorm.DB {
		if scopeSpaceIDs == nil {
			return tx
		}
		return tx.Where("space_id IN ?", scopeSpaceIDs)
	}

	count := func(tx *gorm.DB, dst *int64) error {
		if err := tx.Count(dst).Error; err != nil {
			return fmt.Errorf("统计数据失败: %w", err)
		}
		return nil
	}

	u := func() *gorm.DB { return byDept(s.db.Model(&model.User{})) }
	n := func() *gorm.DB { return bySpace(s.db.Model(&model.Node{})) }

	deptTx := s.db.Model(&model.Department{})
	spaceTx := s.db.Model(&model.Space{})
	if scopeDeptIDs != nil {
		deptTx = deptTx.Where("id IN ?", scopeDeptIDs)
		spaceTx = spaceTx.Where("id IN ?", scopeSpaceIDs)
	}

	for _, step := range []struct {
		tx  *gorm.DB
		dst *int64
	}{
		{u(), &out.UserTotal},
		{u().Where("status = ?", model.UserActive), &out.UserActive},
		{deptTx, &out.DeptTotal},
		{spaceTx, &out.SpaceTotal},
		{n().Where("is_dir = ? AND trashed = ?", false, false), &out.FileTotal},
		{n().Where("is_dir = ? AND trashed = ?", true, false), &out.FolderTotal},
		{n().Where("trashed = ?", true), &out.TrashTotal},
		{bySpace(s.db.Model(&model.Share{})).Where("revoked = ?", false), &out.ShareTotal},
	} {
		if err := count(step.tx, step.dst); err != nil {
			return nil, err
		}
	}

	// 逻辑占用：范围内文件大小之和。
	var logical *int64
	if err := n().Where("is_dir = ?", false).
		Select("COALESCE(SUM(size), 0)").Scan(&logical).Error; err != nil {
		return nil, fmt.Errorf("统计逻辑容量失败: %w", err)
	}
	if logical != nil {
		out.LogicalBytes = *logical
	}
	out.LogicalText = HumanSize(out.LogicalBytes)

	// 物理占用和去重节省是**整个系统**的属性：blob 是全局去重的，
	// 没法拆到某个部门头上。所以只报给超管，部门管理员这两项留空，
	// 界面上不显示——给个按自己范围硬算的数字反而是误导。
	if scopeDeptIDs == nil {
		var stored *int64
		if err := s.db.Model(&model.Blob{}).Select("COALESCE(SUM(size), 0)").Scan(&stored).Error; err != nil {
			return nil, fmt.Errorf("统计存储占用失败: %w", err)
		}
		if stored != nil {
			out.StoredBytes = *stored
		}
		out.DedupSaved = out.LogicalBytes - out.StoredBytes
		if out.DedupSaved < 0 {
			out.DedupSaved = 0
		}
		out.StoredText = HumanSize(out.StoredBytes)
		out.DedupText = HumanSize(out.DedupSaved)
	}

	since := time.Now().Add(-7 * 24 * time.Hour)
	if err := count(n().Where("is_dir = ? AND created_at >= ?", false, since), &out.RecentUploads); err != nil {
		return nil, err
	}

	// 空间用量 Top：受范围限制时只有部门空间和公共空间，
	// 个人空间已经在上面算 scopeSpaceIDs 时排除掉了。
	var spaces []model.Space
	topTx := s.db.Order("used_bytes desc").Limit(8)
	if scopeSpaceIDs != nil {
		topTx = topTx.Where("id IN ?", scopeSpaceIDs)
	}
	if err := topTx.Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("查询空间用量失败: %w", err)
	}
	for _, sp := range spaces {
		out.TopSpaces = append(out.TopSpaces, SpaceUsage{
			ID: sp.ID, Name: sp.Name, Type: string(sp.Type),
			Used: sp.UsedBytes, UsedText: HumanSize(sp.UsedBytes), Quota: sp.QuotaBytes,
		})
	}

	deptUsage, err := s.deptUsage(scopeDeptIDs)
	if err != nil {
		return nil, err
	}
	out.DeptUsage = deptUsage
	return out, nil
}

func (s *StatsService) deptUsage(scopeDeptIDs []uint64) ([]DeptUsageBrief, error) {
	var depts []model.Department
	tx := s.db.Order("depth asc, sort asc").Limit(50)
	if scopeDeptIDs != nil {
		if len(scopeDeptIDs) == 0 {
			return nil, nil
		}
		tx = tx.Where("id IN ?", scopeDeptIDs)
	}
	if err := tx.Find(&depts).Error; err != nil {
		return nil, fmt.Errorf("查询部门失败: %w", err)
	}
	if len(depts) == 0 {
		return nil, nil
	}
	ids := make([]uint64, 0, len(depts))
	for _, d := range depts {
		ids = append(ids, d.ID)
	}

	type usageRow struct {
		DeptID uint64
		Used   int64
	}
	var rows []usageRow
	err := s.db.Model(&model.Space{}).
		Select("dept_id as dept_id, COALESCE(SUM(used_bytes), 0) as used").
		Where("type = ? AND dept_id IN ?", model.SpaceDepartment, ids).
		Group("dept_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("统计部门用量失败: %w", err)
	}
	usage := make(map[uint64]int64, len(rows))
	for _, r := range rows {
		usage[r.DeptID] = r.Used
	}

	type memberRow struct {
		DeptID uint64
		Cnt    int64
	}
	var members []memberRow
	err = s.db.Model(&model.User{}).
		Select("dept_id as dept_id, count(*) as cnt").
		Where("dept_id IN ?", ids).Group("dept_id").Scan(&members).Error
	if err != nil {
		return nil, fmt.Errorf("统计部门人数失败: %w", err)
	}
	memberCount := make(map[uint64]int64, len(members))
	for _, m := range members {
		memberCount[m.DeptID] = m.Cnt
	}

	out := make([]DeptUsageBrief, 0, len(depts))
	for _, d := range depts {
		out = append(out, DeptUsageBrief{
			DeptID: d.ID, Name: d.Name,
			Members: memberCount[d.ID],
			Used:    usage[d.ID], UsedText: HumanSize(usage[d.ID]),
		})
	}
	return out, nil
}
