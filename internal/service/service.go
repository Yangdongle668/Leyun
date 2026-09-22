// Package service 实现乐云企业网盘的业务逻辑。
package service

import (
	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/pkg/jwtx"
	"github.com/Yangdongle668/Leyun/internal/storage"
)

// Registry 汇总全部业务服务，供 API 层注入。
type Registry struct {
	DB      *gorm.DB
	Cfg     *config.Config
	Store   *storage.Store
	JWT     *jwtx.Manager
	ACL     *ACLService
	Dept    *DepartmentService
	User    *UserService
	Space   *SpaceService
	File    *FileService
	Upload  *UploadService
	Share   *ShareService
	Audit   *AuditService
	Setting *SettingService
	Auth    *AuthService
	Stats   *StatsService
	Office  *OfficeService
	APIKey  *APIKeyService
	AI      *AIService
}

// NewRegistry 组装全部服务。
func NewRegistry(db *gorm.DB, cfg *config.Config, store *storage.Store, jwtMgr *jwtx.Manager) *Registry {
	acl := NewACLService(db)
	audit := NewAuditService(db)
	setting := NewSettingService(db)
	space := NewSpaceService(db, acl)
	dept := NewDepartmentService(db, space)
	user := NewUserService(db, cfg, space, acl)
	file := NewFileService(db, cfg, store, acl, space)
	upload := NewUploadService(db, cfg, store, file, acl, space)
	share := NewShareService(db, cfg, acl, file, space)
	auth := NewAuthService(db, cfg, jwtMgr, audit)
	stats := NewStatsService(db, store)
	office := NewOfficeService(cfg, jwtMgr, file, acl, space, audit)
	apiKey := NewAPIKeyService(db)
	ai := NewAIService(db, acl, file, space, user)

	return &Registry{
		DB: db, Cfg: cfg, Store: store, JWT: jwtMgr,
		ACL: acl, Dept: dept, User: user, Space: space, File: file,
		Upload: upload, Share: share, Audit: audit, Setting: setting,
		Auth: auth, Stats: stats, Office: office,
		APIKey: apiKey, AI: ai,
	}
}
