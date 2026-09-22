package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// Recovery 兜住 panic，返回统一错误结构而不是断开连接。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logx.Error("请求处理异常",
					"path", c.Request.URL.Path, "method", c.Request.Method, "err", err)
				response.Fail(c, response.Internal("服务器内部错误"))
			}
		}()
		c.Next()
	}
}

// AccessLog 打印访问日志。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		cost := time.Since(start)
		// 静态资源刷屏没意义，只在 debug 级别关心它们。
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			return
		}
		logx.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"cost_ms", cost.Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}

// CORS 按配置开启跨域。allowOrigins 为空时不做任何处理（同源部署的默认情况）。
func CORS(allowOrigins []string) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]bool, len(allowOrigins))
	for _, o := range allowOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = true
	}
	return func(c *gin.Context) {
		if len(allowOrigins) == 0 {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if origin != "" && (allowAll || allowed[origin]) {
			if allowAll {
				c.Header("Access-Control-Allow-Origin", "*")
			} else {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Vary", "Origin")
			}
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Requested-With")
			c.Header("Access-Control-Max-Age", "86400")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// NoRegister 明确回绝任何自助注册尝试。
//
// 乐云的账号只能由超级管理员开通。这条路由存在的意义是给误访问者一个明确答复，
// 而不是让 /register 落到前端路由上显示一个空白页。
func NoRegister() gin.HandlerFunc {
	return func(c *gin.Context) {
		response.Fail(c, response.Forbidden("本系统不开放自助注册，账号请联系超级管理员开通"))
	}
}
