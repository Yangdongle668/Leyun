// Package response 统一 HTTP 返回结构与业务错误。
package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Body 是所有接口的统一返回体。
type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// PageData 是分页返回体。
type PageData struct {
	List     any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// 业务错误码。与 HTTP 状态码解耦，便于前端按码分支。
const (
	CodeOK           = 0
	CodeBadRequest   = 40000
	CodeUnauthorized = 40100
	CodeForbidden    = 40300
	CodeNotFound     = 40400
	CodeConflict     = 40900
	CodeQuotaExceed  = 41300
	CodeInternal     = 50000
)

// Error 是带业务码的错误。
type Error struct {
	Code    int
	Status  int
	Message string
	cause   error
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.Message }

// Unwrap 暴露底层错误，便于 errors.Is/As。
func (e *Error) Unwrap() error { return e.cause }

// WithCause 附加底层错误（只用于日志，不返回给前端）。
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

func newErr(code, status int, msg string) *Error {
	return &Error{Code: code, Status: status, Message: msg}
}

// BadRequest 构造参数错误。
func BadRequest(msg string) *Error { return newErr(CodeBadRequest, http.StatusBadRequest, msg) }

// Unauthorized 构造未登录错误。
func Unauthorized(msg string) *Error {
	return newErr(CodeUnauthorized, http.StatusUnauthorized, msg)
}

// Forbidden 构造无权限错误。
func Forbidden(msg string) *Error { return newErr(CodeForbidden, http.StatusForbidden, msg) }

// NotFound 构造资源不存在错误。
func NotFound(msg string) *Error { return newErr(CodeNotFound, http.StatusNotFound, msg) }

// Conflict 构造冲突错误（重名等）。
func Conflict(msg string) *Error { return newErr(CodeConflict, http.StatusConflict, msg) }

// QuotaExceeded 构造配额不足错误。
func QuotaExceeded(msg string) *Error {
	return newErr(CodeQuotaExceed, http.StatusRequestEntityTooLarge, msg)
}

// Internal 构造服务端错误。
func Internal(msg string) *Error { return newErr(CodeInternal, http.StatusInternalServerError, msg) }

// OK 返回成功数据。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: CodeOK, Message: "ok", Data: data})
}

// Page 返回分页数据。
func Page(c *gin.Context, list any, total int64, page, pageSize int) {
	OK(c, PageData{List: list, Total: total, Page: page, PageSize: pageSize})
}

// Fail 把任意错误翻译成统一返回体。未知错误一律按 500 处理并隐藏细节。
func Fail(c *gin.Context, err error) {
	var be *Error
	if errors.As(err, &be) {
		c.AbortWithStatusJSON(be.Status, Body{Code: be.Code, Message: be.Message})
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, Body{
		Code:    CodeInternal,
		Message: "服务器内部错误",
	})
}
