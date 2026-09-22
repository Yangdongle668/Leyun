package service

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
)

// 审计动作常量。前端按这些码翻译成中文。
const (
	ActionLogin         = "auth.login"
	ActionLoginFailed   = "auth.login_failed"
	ActionLogout        = "auth.logout"
	ActionChangePwd     = "auth.change_password"
	ActionUserCreate    = "user.create"
	ActionUserUpdate    = "user.update"
	ActionUserDelete    = "user.delete"
	ActionUserResetPwd  = "user.reset_password"
	ActionUserStatus    = "user.status"
	ActionDeptCreate    = "dept.create"
	ActionDeptUpdate    = "dept.update"
	ActionDeptDelete    = "dept.delete"
	ActionFileUpload    = "file.upload"
	ActionFileDownload  = "file.download"
	ActionFilePreview   = "file.preview"
	ActionFileMkdir     = "file.mkdir"
	ActionFileRename    = "file.rename"
	ActionFileMove      = "file.move"
	ActionFileCopy      = "file.copy"
	ActionFileTrash     = "file.trash"
	ActionFileRestore   = "file.restore"
	ActionFilePurge     = "file.purge"
	ActionACLGrant      = "acl.grant"
	ActionACLRevoke     = "acl.revoke"
	ActionShareCreate   = "share.create"
	ActionShareRevoke   = "share.revoke"
	ActionShareAccess   = "share.access"
	ActionSettingUpdate = "setting.update"
	ActionSpaceUpdate   = "space.update"
)

// AuditService 负责写入与查询审计日志。
type AuditService struct {
	db *gorm.DB
}

// NewAuditService 构造审计服务。
func NewAuditService(db *gorm.DB) *AuditService { return &AuditService{db: db} }

// Entry 是一条待写入的审计记录。
type Entry struct {
	UserID     uint64
	Username   string
	DeptID     uint64
	Action     string
	TargetType string
	TargetID   uint64
	Target     string
	Detail     string
	Success    bool
	IP         string
	UserAgent  string
}

// Write 写入审计日志。审计失败不影响主流程，只记一条错误日志。
func (s *AuditService) Write(e Entry) {
	log := &model.AuditLog{
		UserID:     e.UserID,
		Username:   e.Username,
		DeptID:     e.DeptID,
		Action:     e.Action,
		TargetType: e.TargetType,
		TargetID:   e.TargetID,
		Target:     truncate(e.Target, 500),
		Detail:     e.Detail,
		Success:    e.Success,
		IP:         e.IP,
		UserAgent:  truncate(e.UserAgent, 250),
	}
	if err := s.db.Create(log).Error; err != nil {
		logx.Error("写入审计日志失败", "action", e.Action, "err", err)
	}
}

// AuditQuery 是审计日志的查询条件。
type AuditQuery struct {
	UserID   uint64
	Username string
	Action   string
	DeptID   uint64
	Success  *bool
	Keyword  string
	From     string
	To       string
	Page     int
	PageSize int
}

// List 分页查询审计日志。
func (s *AuditService) List(q AuditQuery) ([]model.AuditLog, int64, error) {
	tx := s.db.Model(&model.AuditLog{})
	if q.UserID > 0 {
		tx = tx.Where("user_id = ?", q.UserID)
	}
	if q.Username != "" {
		tx = tx.Where("username LIKE ?", "%"+q.Username+"%")
	}
	if q.Action != "" {
		tx = tx.Where("action = ?", q.Action)
	}
	if q.DeptID > 0 {
		tx = tx.Where("dept_id = ?", q.DeptID)
	}
	if q.Success != nil {
		tx = tx.Where("success = ?", *q.Success)
	}
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		tx = tx.Where("target LIKE ? OR detail LIKE ?", like, like)
	}
	if q.From != "" {
		tx = tx.Where("created_at >= ?", q.From)
	}
	if q.To != "" {
		tx = tx.Where("created_at <= ?", q.To)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计审计日志失败: %w", err)
	}
	page, size := normalizePage(q.Page, q.PageSize)
	var list []model.AuditLog
	err := tx.Order("id desc").Offset((page - 1) * size).Limit(size).Find(&list).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}
	return list, total, nil
}

// Actions 返回系统中出现过的动作列表，供前端筛选下拉框。
func (s *AuditService) Actions() ([]string, error) {
	var out []string
	err := s.db.Model(&model.AuditLog{}).Distinct("action").Order("action asc").Pluck("action", &out).Error
	if err != nil {
		return nil, fmt.Errorf("查询审计动作失败: %w", err)
	}
	return out, nil
}

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
