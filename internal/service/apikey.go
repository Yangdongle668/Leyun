package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// Scope 是 API Key 的能力项。默认什么都不给，全靠显式列举。
type Scope string

const (
	// ScopeIndexRead 枚举文档元数据、读取删除流水。只有元数据，不含文件内容。
	ScopeIndexRead Scope = "index.read"
	// ScopeContentRead 读取文件原始内容。
	//
	// 这是最重的一项：索引程序要建全量知识库，就必然能读到全公司的文件。
	// 签发时务必单独给一把 key，不要和查询侧的 key 混用。
	ScopeContentRead Scope = "content.read"
	// ScopeACLCheck 批量判定某个用户能看到哪些文档。
	// 查询侧只需要这一项——它读不到任何文件内容。
	ScopeACLCheck Scope = "acl.check"
	// ScopeUserRead 读取用户与部门的基本信息，便于把 user_id 映射成人。
	ScopeUserRead Scope = "user.read"
)

// AllScopes 返回全部可签发的能力项，供管理界面渲染。
func AllScopes() []map[string]string {
	return []map[string]string{
		{"code": string(ScopeIndexRead), "label": "枚举文档元数据", "desc": "列目录树、拉增量变更与删除流水，不含文件内容"},
		{"code": string(ScopeContentRead), "label": "读取文件内容", "desc": "下载原始字节。建索引必需，但等于能读到授权范围内的全部文件"},
		{"code": string(ScopeACLCheck), "label": "批量鉴权", "desc": "判定某个用户能看到哪些文档。查询侧只需要这一项"},
		{"code": string(ScopeUserRead), "label": "读取通讯录", "desc": "查用户与部门基本信息，用于把 user_id 映射成人"},
	}
}

func scopeValid(s Scope) bool {
	switch s {
	case ScopeIndexRead, ScopeContentRead, ScopeACLCheck, ScopeUserRead:
		return true
	}
	return false
}

// KeyPrefix 是所有密钥的固定前缀，便于在日志或代码里一眼认出来，
// 也方便将来做密钥泄露扫描。
const KeyPrefix = "lk_"

// APIKeyService 管理机器凭证。
type APIKeyService struct {
	db *gorm.DB
}

// NewAPIKeyService 构造服务。
func NewAPIKeyService(db *gorm.DB) *APIKeyService { return &APIKeyService{db: db} }

// hashKey 计算密钥指纹。
//
// 用 SHA-256 而不是 bcrypt：密钥是 32 字节随机串，没有被字典爆破的余地；
// 而每个请求都要验一次，bcrypt 那几十毫秒会把同步任务直接拖垮。
func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateKeyInput 是签发密钥的入参。
type CreateKeyInput struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// SpaceIDs 为空表示不限空间。
	SpaceIDs []uint64 `json:"space_ids"`
	// ExpireDays 为 0 表示长期有效（不建议）。
	ExpireDays int    `json:"expire_days"`
	Remark     string `json:"remark"`
}

// CreatedKey 是签发结果。明文密钥只在这里出现一次，之后再也取不回来。
type CreatedKey struct {
	Key    string        `json:"key"`
	Record *model.APIKey `json:"record"`
}

// Create 签发一把新密钥。只有超级管理员可以调用。
func (s *APIKeyService) Create(operator *model.User, in CreateKeyInput) (*CreatedKey, error) {
	if operator == nil || !operator.IsSuperAdmin() {
		return nil, response.Forbidden("只有超级管理员可以签发 API 密钥")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, response.BadRequest("请为密钥起一个便于识别的名字")
	}
	if len(in.Scopes) == 0 {
		return nil, response.BadRequest("请至少勾选一项能力")
	}
	scopes := make([]string, 0, len(in.Scopes))
	for _, raw := range in.Scopes {
		sc := Scope(strings.TrimSpace(raw))
		if !scopeValid(sc) {
			return nil, response.BadRequest(fmt.Sprintf("未知的能力项：%s", raw))
		}
		scopes = append(scopes, string(sc))
	}

	secret, err := hashx.RandomToken(32)
	if err != nil {
		return nil, err
	}
	raw := KeyPrefix + secret

	rec := &model.APIKey{
		Name: in.Name,
		// 前缀取到随机段的前 8 位，够在列表里区分，又远不足以还原密钥。
		Prefix:    raw[:len(KeyPrefix)+8],
		KeyHash:   hashKey(raw),
		Scopes:    strings.Join(scopes, ","),
		SpaceIDs:  joinUint64(in.SpaceIDs),
		Enabled:   true,
		CreatedBy: operator.ID,
		Remark:    in.Remark,
	}
	if in.ExpireDays > 0 {
		exp := time.Now().AddDate(0, 0, in.ExpireDays)
		rec.ExpireAt = &exp
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, fmt.Errorf("签发密钥失败: %w", err)
	}
	return &CreatedKey{Key: raw, Record: rec}, nil
}

// List 列出全部密钥（不含明文）。
func (s *APIKeyService) List() ([]model.APIKey, error) {
	var list []model.APIKey
	if err := s.db.Order("id desc").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("查询密钥失败: %w", err)
	}
	return list, nil
}

// SetEnabled 启用或停用密钥。停用立即生效，正在跑的同步任务下一个请求就会被拒。
func (s *APIKeyService) SetEnabled(id uint64, enabled bool) error {
	res := s.db.Model(&model.APIKey{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return fmt.Errorf("更新密钥状态失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return response.NotFound("密钥不存在")
	}
	return nil
}

// Delete 删除密钥。
func (s *APIKeyService) Delete(id uint64) error {
	res := s.db.Delete(&model.APIKey{}, id)
	if res.Error != nil {
		return fmt.Errorf("删除密钥失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return response.NotFound("密钥不存在")
	}
	return nil
}

// KeyContext 是通过鉴权后的密钥上下文，挂在请求上供后续处理器使用。
type KeyContext struct {
	Key *model.APIKey
	// scopes 与 spaceIDs 是解析好的形式，避免每次判断都去切字符串。
	scopes   map[Scope]bool
	spaceIDs map[uint64]bool
}

// Has 判断密钥是否具备某项能力。
func (k *KeyContext) Has(s Scope) bool { return k != nil && k.scopes[s] }

// AllowsSpace 判断密钥能否触碰该空间。未限定空间的密钥对全部空间放行。
func (k *KeyContext) AllowsSpace(spaceID uint64) bool {
	if k == nil {
		return false
	}
	if len(k.spaceIDs) == 0 {
		return true
	}
	return k.spaceIDs[spaceID]
}

// SpaceFilter 返回密钥限定的空间列表；为 nil 表示不限。
func (k *KeyContext) SpaceFilter() []uint64 {
	if k == nil || len(k.spaceIDs) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(k.spaceIDs))
	for id := range k.spaceIDs {
		out = append(out, id)
	}
	return out
}

// ErrKeyInvalid 表示密钥无效、停用或已过期。对外不区分具体原因，避免被用来探测。
var ErrKeyInvalid = errors.New("api key invalid")

// Authenticate 校验密钥并返回上下文。
func (s *APIKeyService) Authenticate(raw, ip string) (*KeyContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, KeyPrefix) {
		return nil, ErrKeyInvalid
	}
	var rec model.APIKey
	if err := s.db.Where("key_hash = ?", hashKey(raw)).First(&rec).Error; err != nil {
		return nil, ErrKeyInvalid
	}
	if !rec.Enabled {
		return nil, ErrKeyInvalid
	}
	if rec.ExpireAt != nil && rec.ExpireAt.Before(time.Now()) {
		return nil, ErrKeyInvalid
	}

	ctx := &KeyContext{
		Key:      &rec,
		scopes:   map[Scope]bool{},
		spaceIDs: map[uint64]bool{},
	}
	for _, sc := range strings.Split(rec.Scopes, ",") {
		if sc = strings.TrimSpace(sc); sc != "" {
			ctx.scopes[Scope(sc)] = true
		}
	}
	for _, id := range splitUint64(rec.SpaceIDs) {
		ctx.spaceIDs[id] = true
	}

	s.touch(&rec, ip)
	return ctx, nil
}

// touch 记录最近一次使用。
//
// 每次请求都写一行会把数据库打满，所以一分钟内只落一次盘——
// 这个字段是给人看"这把钥匙还在不在用"的，不需要秒级精度。
func (s *APIKeyService) touch(rec *model.APIKey, ip string) {
	now := time.Now()
	if rec.LastUsedAt != nil && now.Sub(*rec.LastUsedAt) < time.Minute && rec.LastUsedIP == ip {
		return
	}
	err := s.db.Model(&model.APIKey{}).Where("id = ?", rec.ID).
		Updates(map[string]any{"last_used_at": now, "last_used_ip": ip}).Error
	if err != nil {
		logx.Warn("更新密钥使用时间失败", "key_id", rec.ID, "err", err)
	}
}

func joinUint64(ids []uint64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatUint(id, 10))
	}
	return strings.Join(parts, ",")
}

func splitUint64(raw string) []uint64 {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		if id, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}
