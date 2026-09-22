// Package middleware 提供 Gin 中间件：鉴权、审计、跨域与异常兜底。
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/jwtx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// Gin 上下文键。
const (
	ctxUser    = "leyun.user"
	ctxSubject = "leyun.subject"
)

// Auth 校验令牌并把用户与权限主体放进上下文。
func Auth(db *gorm.DB, jwtMgr *jwtx.Manager, acl *service.ACLService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			response.Fail(c, response.Unauthorized("请先登录"))
			return
		}
		claims, err := jwtMgr.Parse(token)
		if err != nil {
			response.Fail(c, response.Unauthorized("登录已过期，请重新登录"))
			return
		}

		var user model.User
		if err := db.First(&user, claims.UserID).Error; err != nil {
			response.Fail(c, response.Unauthorized("账号不存在或已被删除"))
			return
		}
		if user.Status == model.UserDisabled {
			response.Fail(c, response.Forbidden("账号已停用，请联系管理员"))
			return
		}

		subj, err := acl.LoadSubject(&user)
		if err != nil {
			response.Fail(c, err)
			return
		}
		c.Set(ctxUser, &user)
		c.Set(ctxSubject, subj)
		c.Next()
	}
}

// OptionalAuth 在有令牌时解析用户，没有也放行。分享页需要"登录了就认人，没登录也能看公开分享"。
func OptionalAuth(db *gorm.DB, jwtMgr *jwtx.Manager, acl *service.ACLService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.Next()
			return
		}
		claims, err := jwtMgr.Parse(token)
		if err != nil {
			c.Next()
			return
		}
		var user model.User
		if err := db.First(&user, claims.UserID).Error; err != nil || user.Status == model.UserDisabled {
			c.Next()
			return
		}
		if subj, err := acl.LoadSubject(&user); err == nil {
			c.Set(ctxUser, &user)
			c.Set(ctxSubject, subj)
		}
		c.Next()
	}
}

// RequireSuperAdmin 只放行超级管理员。开通账号、部门管理、系统设置都挂在它后面。
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		if user == nil || !user.IsSuperAdmin() {
			response.Fail(c, response.Forbidden("该操作仅限超级管理员"))
			return
		}
		c.Next()
	}
}

// RequireAdmin 放行超级管理员与部门管理员（用于只读的管理页面）。
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)
		if user == nil || (user.Role != model.RoleSuperAdmin && user.Role != model.RoleDeptAdmin) {
			response.Fail(c, response.Forbidden("该操作需要管理员权限"))
			return
		}
		c.Next()
	}
}

// RequirePasswordChanged 在开启强制改密时，拦住仍在用默认口令的账号。
func RequirePasswordChanged(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}
		user := CurrentUser(c)
		if user != nil && user.MustChangePassword {
			response.Fail(c, response.Forbidden("请先修改初始口令后再使用系统"))
			return
		}
		c.Next()
	}
}

// CurrentUser 取出当前登录用户，未登录时返回 nil。
func CurrentUser(c *gin.Context) *model.User {
	v, ok := c.Get(ctxUser)
	if !ok {
		return nil
	}
	u, _ := v.(*model.User)
	return u
}

// CurrentSubject 取出当前权限主体，未登录时返回 nil。
func CurrentSubject(c *gin.Context) *service.Subject {
	v, ok := c.Get(ctxSubject)
	if !ok {
		return nil
	}
	s, _ := v.(*service.Subject)
	return s
}

// extractToken 依次从 Authorization 头、查询参数与 Cookie 中取令牌。
//
// 下载、预览这类会被 <a>/<img> 直接发起的请求带不上自定义请求头，
// 所以额外支持 ?token= 形式。
func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(h)
	}
	if t := c.Query("token"); t != "" {
		return t
	}
	if t, err := c.Cookie("leyun_token"); err == nil {
		return t
	}
	return ""
}
