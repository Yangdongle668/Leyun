package service

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/jwtx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// AuthService 负责登录鉴权。
//
// 注意：这里没有 Register。乐云的账号只能由超级管理员在管理后台开通，
// 系统对外不暴露任何自助注册入口。
type AuthService struct {
	db    *gorm.DB
	cfg   *config.Config
	jwt   *jwtx.Manager
	audit *AuditService
}

// NewAuthService 构造认证服务。
func NewAuthService(db *gorm.DB, cfg *config.Config, jwtMgr *jwtx.Manager, audit *AuditService) *AuthService {
	return &AuthService{db: db, cfg: cfg, jwt: jwtMgr, audit: audit}
}

// LoginResult 是登录成功后的返回。
type LoginResult struct {
	Token     string      `json:"token"`
	ExpireAt  time.Time   `json:"expire_at"`
	User      *model.User `json:"user"`
	MustReset bool        `json:"must_reset_password"`
}

// Login 校验账号口令并签发令牌。
func (s *AuthService) Login(username, password, ip, ua string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, response.BadRequest("请输入用户名与口令")
	}

	var u model.User
	err := s.db.Where("username = ?", username).First(&u).Error
	if err != nil {
		s.audit.Write(Entry{
			Username: username, Action: ActionLoginFailed, Success: false,
			Detail: "用户名不存在", IP: ip, UserAgent: ua,
		})
		// 不区分"用户名不存在"和"口令错误"，避免被用来枚举账号。
		return nil, response.Unauthorized("用户名或口令不正确")
	}

	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		remain := time.Until(*u.LockedUntil).Round(time.Minute)
		if remain < time.Minute {
			remain = time.Minute
		}
		s.audit.Write(Entry{
			UserID: u.ID, Username: u.Username, DeptID: u.DeptID,
			Action: ActionLoginFailed, Success: false, Detail: "账号锁定中", IP: ip, UserAgent: ua,
		})
		return nil, response.Forbidden(fmt.Sprintf("账号已锁定，请 %.0f 分钟后再试", remain.Minutes()))
	}

	if !hashx.VerifyPassword(u.PasswordHash, password) {
		s.recordFailure(&u)
		s.audit.Write(Entry{
			UserID: u.ID, Username: u.Username, DeptID: u.DeptID,
			Action: ActionLoginFailed, Success: false, Detail: "口令错误", IP: ip, UserAgent: ua,
		})
		return nil, response.Unauthorized("用户名或口令不正确")
	}

	if u.Status == model.UserDisabled {
		s.audit.Write(Entry{
			UserID: u.ID, Username: u.Username, DeptID: u.DeptID,
			Action: ActionLoginFailed, Success: false, Detail: "账号已停用", IP: ip, UserAgent: ua,
		})
		return nil, response.Forbidden("账号已停用，请联系管理员")
	}

	token, exp, err := s.jwt.Issue(u.ID, u.Username, string(u.Role), u.DeptID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	err = s.db.Model(&model.User{}).Where("id = ?", u.ID).Updates(map[string]any{
		"last_login_at": now, "last_login_ip": ip, "login_failures": 0, "locked_until": nil,
	}).Error
	if err != nil {
		return nil, fmt.Errorf("更新登录信息失败: %w", err)
	}
	u.LastLoginAt = &now
	u.LastLoginIP = ip

	s.audit.Write(Entry{
		UserID: u.ID, Username: u.Username, DeptID: u.DeptID,
		Action: ActionLogin, Success: true, IP: ip, UserAgent: ua,
	})
	return &LoginResult{Token: token, ExpireAt: exp, User: &u, MustReset: u.MustChangePassword}, nil
}

// recordFailure 累计登录失败次数，超过阈值就临时锁定。
func (s *AuthService) recordFailure(u *model.User) {
	failures := u.LoginFailures + 1
	updates := map[string]any{"login_failures": failures}
	if s.cfg.Security.MaxLoginFailures > 0 && failures >= s.cfg.Security.MaxLoginFailures {
		until := time.Now().Add(s.cfg.Security.LoginLockDuration)
		updates["locked_until"] = until
		updates["login_failures"] = 0
	}
	if err := s.db.Model(&model.User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
		// 失败计数写不进去不该拦住正常的登录拒绝流程，记一笔即可。
		_ = err
	}
}

// Refresh 用当前令牌换一个新的，延长在线时间。
func (s *AuthService) Refresh(u *model.User) (*LoginResult, error) {
	if u.Status == model.UserDisabled {
		return nil, response.Forbidden("账号已停用")
	}
	token, exp, err := s.jwt.Issue(u.ID, u.Username, string(u.Role), u.DeptID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpireAt: exp, User: u, MustReset: u.MustChangePassword}, nil
}
