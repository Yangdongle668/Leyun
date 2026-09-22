// Package handler 实现 HTTP 接口。
package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/api/middleware"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// Handler 持有全部服务依赖。
type Handler struct {
	svc *service.Registry
}

// New 构造 Handler。
func New(svc *service.Registry) *Handler { return &Handler{svc: svc} }

// audit 记录一条审计日志，自动带上当前用户与请求信息。
func (h *Handler) audit(c *gin.Context, action, targetType string, targetID uint64, target, detail string, success bool) {
	entry := service.Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Target:     target,
		Detail:     detail,
		Success:    success,
		IP:         c.ClientIP(),
		UserAgent:  c.Request.UserAgent(),
	}
	if u := middleware.CurrentUser(c); u != nil {
		entry.UserID = u.ID
		entry.Username = u.Username
		entry.DeptID = u.DeptID
	}
	h.svc.Audit.Write(entry)
}

// subject 取当前权限主体；理论上中间件已经放好，兜底返回 401。
func (h *Handler) subject(c *gin.Context) (*service.Subject, bool) {
	subj := middleware.CurrentSubject(c)
	if subj == nil || subj.User == nil {
		response.Fail(c, response.Unauthorized("请先登录"))
		return nil, false
	}
	return subj, true
}

// bind 解析 JSON 请求体。
func bind[T any](c *gin.Context) (*T, bool) {
	var body T
	if err := c.ShouldBindJSON(&body); err != nil {
		response.Fail(c, response.BadRequest("请求参数格式不正确"))
		return nil, false
	}
	return &body, true
}

// uintParam 解析路径参数中的 ID。
func uintParam(c *gin.Context, name string) (uint64, bool) {
	raw := c.Param(name)
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		response.Fail(c, response.BadRequest("非法的 "+name))
		return 0, false
	}
	return v, true
}

// uintQuery 解析查询参数中的无符号整数，缺省返回 def。
func uintQuery(c *gin.Context, name string, def uint64) uint64 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// intQuery 解析查询参数中的整数。
func intQuery(c *gin.Context, name string, def int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// boolQuery 解析查询参数中的布尔值。
func boolQuery(c *gin.Context, name string, def bool) bool {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}
