package service

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// SettingService 读写运行期系统配置。
type SettingService struct {
	db *gorm.DB
}

// NewSettingService 构造配置服务。
func NewSettingService(db *gorm.DB) *SettingService { return &SettingService{db: db} }

// All 返回全部配置项。
func (s *SettingService) All() (map[string]string, error) {
	var list []model.Setting
	if err := s.db.Find(&list).Error; err != nil {
		return nil, fmt.Errorf("读取系统配置失败: %w", err)
	}
	out := make(map[string]string, len(list))
	for _, item := range list {
		out[item.Key] = item.Value
	}
	return out, nil
}

// AllSafe 返回可以交给前端的配置：秘密项被替换成掩码。
//
// 管理页面一律用这个，不要用 All()。
func (s *SettingService) AllSafe() (map[string]string, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	for k, v := range all {
		if secretSettings[k] {
			all[k] = MaskSecret(v)
		}
	}
	return all, nil
}

// Get 读取单个配置，缺失时返回 def。
func (s *SettingService) Get(key, def string) string {
	var item model.Setting
	if err := s.db.Where("config_key = ?", key).First(&item).Error; err != nil {
		return def
	}
	return item.Value
}

// GetInt64 读取整型配置。
func (s *SettingService) GetInt64(key string, def int64) int64 {
	v := s.Get(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// GetBool 读取布尔配置。
func (s *SettingService) GetBool(key string, def bool) bool {
	v := s.Get(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// 允许通过管理后台修改的配置白名单。未列出的键一律拒绝，避免前端写入任意键值。
var editableSettings = map[string]bool{
	store.SettingSiteName:           true,
	store.SettingDefaultUserQuota:   true,
	store.SettingDefaultDeptQuota:   true,
	store.SettingAllowPublicShare:   true,
	store.SettingTrashRetentionDays: true,

	store.SettingAIEnabled:         true,
	store.SettingAIBaseURL:         true,
	store.SettingAIAPIKey:          true,
	store.SettingAIChatModel:       true,
	store.SettingAIEmbedModel:      true,
	store.SettingAIEmbedDim:        true,
	store.SettingAIEmbedBatch:      true,
	store.SettingAIChunkSize:       true,
	store.SettingAIChunkOverlap:    true,
	store.SettingAITopK:            true,
	store.SettingAISpaceIDs:        true,
	store.SettingAIIncludePersonal: true,
	store.SettingAIMaxFileSize:     true,

	store.SettingTLSEnabled:      true,
	store.SettingTLSDomains:      true,
	store.SettingTLSEmail:        true,
	store.SettingTLSDirectoryURL: true,
	store.SettingTLSRedirect:     true,
	store.SettingTLSAgreedAt:     true,
}

// 秘密配置项：可以写进去，但绝不能再读出来给前端。
//
// 原先管理页面直接把 All() 的结果整个发给浏览器，令牌签名密钥也在里面——
// 拿到它就能伪造任意账号（包括超管）的登录令牌，而且改密、停用都拦不住，
// 只有轮换密钥才能止血。秘密在服务层就挡掉，比指望每个调用方记得过滤可靠。
var secretSettings = map[string]bool{
	store.SettingJWTSecret: true,
	store.SettingAIAPIKey:  true,
}

// IsSecret 判断某个配置项是否属于不可回显的秘密。
func IsSecret(key string) bool { return secretSettings[key] }

// MaskSecret 把秘密压成可供人辨认、但不足以使用的形式。
//
// 只保留尾部 4 位：够管理员确认"配的是这把"，又不足以拼回原值。
func MaskSecret(v string) string {
	if v == "" {
		return ""
	}
	r := []rune(v)
	if len(r) <= 4 {
		return "****"
	}
	return "****" + string(r[len(r)-4:])
}

// Set 更新（或新建）一个配置项。
func (s *SettingService) Set(key, value string) error {
	if !editableSettings[key] {
		return fmt.Errorf("配置项 %s 不允许修改", key)
	}
	// 秘密项前端拿到的是掩码，原样提交回来意味着"这一项没动"。
	// 不挡住的话，管理员改个站点名就会把 API Key 覆盖成 "****abcd"。
	if secretSettings[key] && value != "" && value == MaskSecret(s.Get(key, "")) {
		return nil
	}
	return s.forceSet(key, value)
}

// forceSet 绕过白名单直接落库，供系统自身写入配置（如自动生成的密钥、证书状态）。
// 它不检查白名单，所以绝不能直接接到 HTTP 入参上。
func (s *SettingService) forceSet(key, value string) error {
	var item model.Setting
	err := s.db.Where("config_key = ?", key).First(&item).Error
	switch {
	case err == nil:
		item.Value = value
		item.UpdatedAt = time.Now()
		if err := s.db.Save(&item).Error; err != nil {
			return fmt.Errorf("更新系统配置失败: %w", err)
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		item = model.Setting{Key: key, Value: value, UpdatedAt: time.Now()}
		if err := s.db.Create(&item).Error; err != nil {
			return fmt.Errorf("写入系统配置失败: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("读取系统配置失败: %w", err)
	}
}

// SetMany 批量更新配置，忽略白名单外的键。
func (s *SettingService) SetMany(values map[string]string) error {
	for k, v := range values {
		if !editableSettings[k] {
			continue
		}
		if err := s.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}

// Public 返回无需登录即可读取的配置（登录页需要站点名）。
func (s *SettingService) Public() map[string]any {
	return map[string]any{
		"site_name": s.Get(store.SettingSiteName, "乐云企业网盘"),
	}
}
