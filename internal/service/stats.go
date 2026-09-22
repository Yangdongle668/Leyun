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
func (s *StatsService) Overview() (*Overview, error) {
	out := &Overview{}

	counts := []struct {
		model any
		where []any
		dst   *int64
	}{
		{&model.User{}, nil, &out.UserTotal},
		{&model.User{}, []any{"status = ?", model.UserActive}, &out.UserActive},
		{&model.Department{}, nil, &out.DeptTotal},
		{&model.Space{}, nil, &out.SpaceTotal},
		{&model.Node{}, []any{"is_dir = ? AND trashed = ?", false, false}, &out.FileTotal},
		{&model.Node{}, []any{"is_dir = ? AND trashed = ?", true, false}, &out.FolderTotal},
		{&model.Node{}, []any{"trashed = ?", true}, &out.TrashTotal},
		{&model.Share{}, []any{"revoked = ?", false}, &out.ShareTotal},
	}
	for _, c := range counts {
		tx := s.db.Model(c.model)
		if len(c.where) > 0 {
			tx = tx.Where(c.where[0], c.where[1:]...)
		}
		if err := tx.Count(c.dst).Error; err != nil {
			return nil, fmt.Errorf("统计数据失败: %w", err)
		}
	}

	// 物理占用（去重后）与逻辑占用（各空间累加）的差值就是去重省下的空间。
	var stored *int64
	if err := s.db.Model(&model.Blob{}).Select("COALESCE(SUM(size), 0)").Scan(&stored).Error; err != nil {
		return nil, fmt.Errorf("统计存储占用失败: %w", err)
	}
	if stored != nil {
		out.StoredBytes = *stored
	}
	var logical *int64
	if err := s.db.Model(&model.Node{}).Where("is_dir = ?", false).
		Select("COALESCE(SUM(size), 0)").Scan(&logical).Error; err != nil {
		return nil, fmt.Errorf("统计逻辑容量失败: %w", err)
	}
	if logical != nil {
		out.LogicalBytes = *logical
	}
	out.DedupSaved = out.LogicalBytes - out.StoredBytes
	if out.DedupSaved < 0 {
		out.DedupSaved = 0
	}
	out.StoredText = HumanSize(out.StoredBytes)
	out.LogicalText = HumanSize(out.LogicalBytes)
	out.DedupText = HumanSize(out.DedupSaved)

	since := time.Now().Add(-7 * 24 * time.Hour)
	if err := s.db.Model(&model.Node{}).
		Where("is_dir = ? AND created_at >= ?", false, since).
		Count(&out.RecentUploads).Error; err != nil {
		return nil, fmt.Errorf("统计近期上传失败: %w", err)
	}

	var spaces []model.Space
	if err := s.db.Order("used_bytes desc").Limit(8).Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("查询空间用量失败: %w", err)
	}
	for _, sp := range spaces {
		out.TopSpaces = append(out.TopSpaces, SpaceUsage{
			ID: sp.ID, Name: sp.Name, Type: string(sp.Type),
			Used: sp.UsedBytes, UsedText: HumanSize(sp.UsedBytes), Quota: sp.QuotaBytes,
		})
	}

	deptUsage, err := s.deptUsage()
	if err != nil {
		return nil, err
	}
	out.DeptUsage = deptUsage
	return out, nil
}

func (s *StatsService) deptUsage() ([]DeptUsageBrief, error) {
	var depts []model.Department
	if err := s.db.Order("depth asc, sort asc").Limit(50).Find(&depts).Error; err != nil {
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
