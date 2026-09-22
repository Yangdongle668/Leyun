// Package config 负责加载与校验乐云（Leyun）企业网盘的运行配置。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是应用的全量配置。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Storage  StorageConfig  `yaml:"storage"`
	JWT      JWTConfig      `yaml:"jwt"`
	Security SecurityConfig `yaml:"security"`
	Office   OfficeConfig   `yaml:"office"`
	Log      LogConfig      `yaml:"log"`
}

// OfficeConfig 是 Office 在线编辑（ONLYOFFICE Document Server）的对接配置。
//
// 这里有两个地址，容易混：
//   - PublicURL 是浏览器去加载编辑器 JS 的地址，必须是用户电脑能打开的；
//   - InternalURL 是乐云后端回连 Document Server 取文件用的地址，容器内网地址即可。
//
// 同理 CallbackBase 是 Document Server 回调乐云的地址，填容器网络里的服务名。
type OfficeConfig struct {
	Enabled     bool   `yaml:"enabled"`
	PublicURL   string `yaml:"public_url"`
	InternalURL string `yaml:"internal_url"`
	// CallbackBase 为空时按请求的 Host 推断，单机部署可以不填。
	CallbackBase string `yaml:"callback_base"`
	// JWTSecret 必须与 Document Server 的 JWT_SECRET 一致；为空表示不启用签名校验。
	JWTSecret string `yaml:"jwt_secret"`
	// PDFEdit 控制 PDF 走哪条路：
	//
	//   true  —— documentType=pdf，按 ACL 决定能否编辑（改文字、批注、填表单）。
	//            需要 Document Server 8.1 及以上，本项目的 compose 固定 8.2。
	//   false —— documentType=word 的旧式只读预览，兼容 8.1 之前的 Document Server。
	//
	// 无论取值如何，浏览器内置的 PDF.js 阅读器都照常可用，不依赖 Document Server。
	PDFEdit bool `yaml:"pdf_edit"`
	// TokenTTL 是下发给 Document Server 的一次性资源令牌有效期。
	TokenTTL time.Duration `yaml:"token_ttl"`
	Lang     string        `yaml:"lang"`
}

// ServerConfig 是 HTTP 服务配置。
type ServerConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Mode            string        `yaml:"mode"` // debug / release
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// AllowOrigins 为空表示不额外开启跨域（前端与后端同源部署时的默认值）。
	AllowOrigins []string `yaml:"allow_origins"`
}

// DatabaseConfig 是数据库配置，driver 支持 sqlite / mysql / postgres。
type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
	// SlowThreshold 超过该耗时的 SQL 会打印慢查询日志。
	SlowThreshold time.Duration `yaml:"slow_threshold"`
	MaxOpenConns  int           `yaml:"max_open_conns"`
	MaxIdleConns  int           `yaml:"max_idle_conns"`
}

// StorageConfig 是文件落盘相关配置。
type StorageConfig struct {
	// Root 是 blob（去重后的真实文件）存放目录。
	Root string `yaml:"root"`
	// CertRoot 是自动申请来的 HTTPS 证书与 ACME 账号密钥的存放目录。
	// 留空则取 Root 的同级目录 certs。
	CertRoot string `yaml:"cert_root"`
	// TempRoot 是分片上传的临时目录。
	TempRoot string `yaml:"temp_root"`
	// ChunkSize 是建议的分片大小，前端按此值切片。
	ChunkSize int64 `yaml:"chunk_size"`
	// MaxUploadSize 单文件上限，0 表示不限制。
	MaxUploadSize int64 `yaml:"max_upload_size"`
	// UploadSessionTTL 分片会话的有效期，过期后由定时任务清理。
	UploadSessionTTL time.Duration `yaml:"upload_session_ttl"`
	// TrashRetention 回收站保留时长，0 表示永久保留直到手动清空。
	TrashRetention time.Duration `yaml:"trash_retention"`
}

// JWTConfig 是令牌签发配置。
type JWTConfig struct {
	Secret  string        `yaml:"secret"`
	Issuer  string        `yaml:"issuer"`
	Expire  time.Duration `yaml:"expire"`
	Refresh time.Duration `yaml:"refresh"`
}

// SecurityConfig 汇总账号安全策略。
//
// 乐云不提供自助注册：账号只能由超级管理员开通，这一点由 API 层强制，
// 此处的配置只用于约束密码强度与默认超管账号的行为。
type SecurityConfig struct {
	// DefaultAdminUsername / DefaultAdminPassword 是首次初始化时写入的超级管理员账号。
	DefaultAdminUsername string `yaml:"default_admin_username"`
	DefaultAdminPassword string `yaml:"default_admin_password"`
	// ForceChangeDefaultPassword 为 true 时，默认口令未修改前只能调用改密接口。
	ForceChangeDefaultPassword bool `yaml:"force_change_default_password"`
	// PasswordMinLength 新密码的最小长度。
	PasswordMinLength int `yaml:"password_min_length"`
	// MaxLoginFailures 连续登录失败上限，达到后锁定 LoginLockDuration。0 表示不锁定。
	MaxLoginFailures  int           `yaml:"max_login_failures"`
	LoginLockDuration time.Duration `yaml:"login_lock_duration"`
	// DefaultUserQuota 新建用户的个人空间配额，0 表示不限制。
	DefaultUserQuota int64 `yaml:"default_user_quota"`
	// DefaultDeptQuota 新建部门空间的配额，0 表示不限制。
	DefaultDeptQuota int64 `yaml:"default_dept_quota"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level  string `yaml:"level"`  // debug/info/warn/error
	Format string `yaml:"format"` // text/json
}

// Default 返回一份可直接启动的配置：SQLite + 本地磁盘 + 默认超管 admin/admin。
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			Mode:            "release",
			ShutdownTimeout: 10 * time.Second,
		},
		Database: DatabaseConfig{
			Driver:        "sqlite",
			DSN:           "data/leyun.db",
			SlowThreshold: 500 * time.Millisecond,
			MaxOpenConns:  50,
			MaxIdleConns:  10,
		},
		Storage: StorageConfig{
			Root:             "data/blobs",
			TempRoot:         "data/tmp",
			ChunkSize:        8 << 20,
			MaxUploadSize:    0,
			UploadSessionTTL: 24 * time.Hour,
			TrashRetention:   30 * 24 * time.Hour,
		},
		JWT: JWTConfig{
			Issuer:  "leyun",
			Expire:  12 * time.Hour,
			Refresh: 7 * 24 * time.Hour,
		},
		Security: SecurityConfig{
			DefaultAdminUsername:       "admin",
			DefaultAdminPassword:       "admin",
			ForceChangeDefaultPassword: false,
			PasswordMinLength:          5,
			MaxLoginFailures:           10,
			LoginLockDuration:          15 * time.Minute,
			DefaultUserQuota:           10 << 30,
			DefaultDeptQuota:           100 << 30,
		},
		Office: OfficeConfig{
			Enabled:  false,
			PDFEdit:  true,
			TokenTTL: 12 * time.Hour,
			Lang:     "zh-CN",
		},
		Log: LogConfig{Level: "info", Format: "text"},
	}
}

// Load 读取 YAML 配置文件；path 为空或文件不存在时回落到默认配置。
// 读取完成后会套用 LEYUN_* 环境变量覆盖，便于容器化部署。
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(raw, cfg); err != nil {
				return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
			}
		case os.IsNotExist(err):
			// 保持默认配置，首次运行无需准备任何文件。
		default:
			return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
		}
	}
	cfg.applyEnv()
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	setString(&c.Server.Host, "LEYUN_SERVER_HOST")
	setInt(&c.Server.Port, "LEYUN_SERVER_PORT")
	setString(&c.Server.Mode, "LEYUN_SERVER_MODE")
	setString(&c.Database.Driver, "LEYUN_DB_DRIVER")
	setString(&c.Database.DSN, "LEYUN_DB_DSN")
	setString(&c.Storage.Root, "LEYUN_STORAGE_ROOT")
	setString(&c.Storage.TempRoot, "LEYUN_STORAGE_TEMP")
	setString(&c.JWT.Secret, "LEYUN_JWT_SECRET")
	setString(&c.Security.DefaultAdminUsername, "LEYUN_ADMIN_USERNAME")
	setString(&c.Security.DefaultAdminPassword, "LEYUN_ADMIN_PASSWORD")
	setString(&c.Log.Level, "LEYUN_LOG_LEVEL")
	setString(&c.Office.PublicURL, "LEYUN_OFFICE_PUBLIC_URL")
	setString(&c.Office.InternalURL, "LEYUN_OFFICE_INTERNAL_URL")
	setString(&c.Office.CallbackBase, "LEYUN_OFFICE_CALLBACK_BASE")
	setString(&c.Office.JWTSecret, "LEYUN_OFFICE_JWT_SECRET")
	setBool(&c.Office.Enabled, "LEYUN_OFFICE_ENABLED")
	setBool(&c.Office.PDFEdit, "LEYUN_OFFICE_PDF_EDIT")
	if v := os.Getenv("LEYUN_ALLOW_ORIGINS"); v != "" {
		c.Server.AllowOrigins = splitAndTrim(v)
	}
}

func (c *Config) normalize() error {
	c.Database.Driver = strings.ToLower(strings.TrimSpace(c.Database.Driver))
	switch c.Database.Driver {
	case "sqlite", "mysql", "postgres":
	case "":
		c.Database.Driver = "sqlite"
	default:
		return fmt.Errorf("不支持的数据库驱动: %s（可选 sqlite/mysql/postgres）", c.Database.Driver)
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("非法的监听端口: %d", c.Server.Port)
	}
	if c.Storage.Root == "" {
		c.Storage.Root = "data/blobs"
	}
	if c.Storage.TempRoot == "" {
		c.Storage.TempRoot = "data/tmp"
	}
	if c.Storage.ChunkSize <= 0 {
		c.Storage.ChunkSize = 8 << 20
	}
	if c.Storage.UploadSessionTTL <= 0 {
		c.Storage.UploadSessionTTL = 24 * time.Hour
	}
	if c.JWT.Expire <= 0 {
		c.JWT.Expire = 12 * time.Hour
	}
	if c.JWT.Refresh < c.JWT.Expire {
		c.JWT.Refresh = c.JWT.Expire
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = "leyun"
	}
	if c.Security.DefaultAdminUsername == "" {
		c.Security.DefaultAdminUsername = "admin"
	}
	if c.Security.DefaultAdminPassword == "" {
		c.Security.DefaultAdminPassword = "admin"
	}
	if c.Security.PasswordMinLength <= 0 {
		// 默认超管口令为 admin（5 位），最小长度不能高于它，否则首次改密会自相矛盾。
		c.Security.PasswordMinLength = 5
	}
	if c.Security.LoginLockDuration <= 0 {
		c.Security.LoginLockDuration = 15 * time.Minute
	}
	c.Office.PublicURL = strings.TrimRight(strings.TrimSpace(c.Office.PublicURL), "/")
	c.Office.InternalURL = strings.TrimRight(strings.TrimSpace(c.Office.InternalURL), "/")
	c.Office.CallbackBase = strings.TrimRight(strings.TrimSpace(c.Office.CallbackBase), "/")
	if c.Office.InternalURL == "" {
		// 单机部署时浏览器与后端看到的是同一个 Document Server 地址。
		c.Office.InternalURL = c.Office.PublicURL
	}
	if c.Office.TokenTTL <= 0 {
		c.Office.TokenTTL = 12 * time.Hour
	}
	if c.Office.Lang == "" {
		c.Office.Lang = "zh-CN"
	}
	if c.Office.Enabled && c.Office.PublicURL == "" {
		return fmt.Errorf("已开启 Office 在线编辑，但未配置 office.public_url")
	}

	abs, err := filepath.Abs(c.Storage.Root)
	if err != nil {
		return fmt.Errorf("解析存储目录失败: %w", err)
	}
	c.Storage.Root = abs
	if abs, err = filepath.Abs(c.Storage.TempRoot); err != nil {
		return fmt.Errorf("解析临时目录失败: %w", err)
	}
	c.Storage.TempRoot = abs
	return nil
}

// Addr 返回 net/http 监听地址。
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// CertDir 返回证书缓存目录。
//
// 默认放在 blob 目录的同级而不是里面：blob 目录会被备份脚本整个打包，
// 私钥混在里面容易跟着到处跑。
func (s StorageConfig) CertDir() string {
	if strings.TrimSpace(s.CertRoot) != "" {
		return s.CertRoot
	}
	return filepath.Join(filepath.Dir(strings.TrimRight(s.Root, string(filepath.Separator))), "certs")
}

func setString(dst *string, env string) {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		*dst = v
	}
}

func setBool(dst *bool, env string) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(env)))
	switch v {
	case "1", "true", "yes", "on":
		*dst = true
	case "0", "false", "no", "off":
		*dst = false
	}
}

func setInt(dst *int, env string) {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
		*dst = n
	}
}

func splitAndTrim(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
