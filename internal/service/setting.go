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
}

// Set 更新（或新建）一个配置项。
func (s *SettingService) Set(key, value string) error {
	if !editableSettings[key] {
		return fmt.Errorf("配置项 %s 不允许修改", key)
	}
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
