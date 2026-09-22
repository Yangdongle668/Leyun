package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

const ctxAPIKey = "leyun.apikey"

// APIKeyAuth 校验机器凭证。
//
// 刻意不接受用户令牌：开放接口是给程序用的，能力由密钥的 scope 显式限定；
// 允许用浏览器里的登录态直接调，等于把这套限制绕过去了。
func APIKeyAuth(keys *service.APIKeyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractAPIKey(c)
		if raw == "" {
			response.Fail(c, response.Unauthorized("缺少 API 密钥"))
			return
		}
		ctx, err := keys.Authenticate(raw, c.ClientIP())
		if err != nil {
			// 不区分"不存在/已停用/已过期"，免得被拿来探测密钥是否有效。
			response.Fail(c, response.Unauthorized("API 密钥无效、已停用或已过期"))
			return
		}
		c.Set(ctxAPIKey, ctx)
		c.Next()
	}
}

// RequireScope 要求密钥具备指定能力。
func RequireScope(scope service.Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := CurrentAPIKey(c)
		if key == nil || !key.Has(scope) {
			response.Fail(c, response.Forbidden("该密钥没有“"+string(scope)+"”能力"))
			return
		}
		c.Next()
	}
}

// CurrentAPIKey 取出当前请求的密钥上下文。
func CurrentAPIKey(c *gin.Context) *service.KeyContext {
	v, ok := c.Get(ctxAPIKey)
	if !ok {
		return nil
	}
	k, _ := v.(*service.KeyContext)
	return k
}

// extractAPIKey 从请求里取密钥。
//
// 支持 Authorization: Bearer 与 X-API-Key 两种写法——前者是通例，
// 后者在一些 HTTP 客户端和网关里更省事。不支持放进 URL 查询串：
// 那会被完整记进各级访问日志。
func extractAPIKey(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-API-Key")); v != "" {
		return v
	}
	if h := c.GetHeader("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(h)
	}
	return ""
}
