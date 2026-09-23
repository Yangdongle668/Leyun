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

// wakeIndexer 让知识库立刻去扫一遍，而不是干等下一轮轮询。
//
// 索引协程每 2 分钟兜底扫一次全量，所以不叫醒它文件终究也会被索引——
// 只是上传完要等上两分钟才搜得到，用起来就像"自动索引没生效"。
//
// 叫醒是一次非阻塞的 channel 投递，通道容量为 1：批量上传几十个文件时
// 会自然合并成一次，不会把索引协程刷爆。哪些文件该进索引由索引协程自己
// 按配置判断（个人空间默认不进），这里只负责催一下。
func (h *Handler) wakeIndexer() {
	h.svc.KB.Wake()
}

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
