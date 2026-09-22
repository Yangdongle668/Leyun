// Package jwtx 负责签发与校验访问令牌。
package jwtx

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是乐云的令牌载荷。
type Claims struct {
	UserID   uint64 `json:"uid"`
	Username string `json:"uname"`
	Role     string `json:"role"`
	DeptID   uint64 `json:"dept"`
	jwt.RegisteredClaims
}

// Manager 负责令牌的签发与解析。
type Manager struct {
	secret  []byte
	issuer  string
	expire  time.Duration
	refresh time.Duration
}

// New 构造令牌管理器。
func New(secret, issuer string, expire, refresh time.Duration) *Manager {
	return &Manager{secret: []byte(secret), issuer: issuer, expire: expire, refresh: refresh}
}

// Expire 返回访问令牌有效期。
func (m *Manager) Expire() time.Duration { return m.expire }

// Issue 签发访问令牌。
func (m *Manager) Issue(userID uint64, username, role string, deptID uint64) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(m.expire)
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		DeptID:   deptID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("签发令牌失败: %w", err)
	}
	return signed, exp, nil
}

// ErrInvalidToken 表示令牌无效或已过期。
var ErrInvalidToken = errors.New("令牌无效或已过期")

// Parse 解析并校验令牌。
func (m *Manager) Parse(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("不支持的签名算法: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ResourceClaims 是一次性资源令牌的载荷。
//
// Office 在线编辑时，Document Server 是独立进程，它拿不到用户浏览器里的登录态，
// 只能靠乐云下发的这个短时令牌来取文件、回写保存。
type ResourceClaims struct {
	NodeID uint64 `json:"nid"`
	UserID uint64 `json:"uid"`
	Scope  string `json:"scope"`
	jwt.RegisteredClaims
}

// IssueResource 签发资源令牌。
func (m *Manager) IssueResource(nodeID, userID uint64, scope string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := &ResourceClaims{
		NodeID: nodeID,
		UserID: userID,
		Scope:  scope,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   fmt.Sprintf("res:%d", nodeID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("签发资源令牌失败: %w", err)
	}
	return signed, nil
}

// ParseResource 解析资源令牌，并校验 scope 是否匹配。
func (m *Manager) ParseResource(token, wantScope string) (*ResourceClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, &ResourceClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("不支持的签名算法: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := parsed.Claims.(*ResourceClaims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if wantScope != "" && claims.Scope != wantScope {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
