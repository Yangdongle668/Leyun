// Package api 组装 HTTP 路由。
package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/api/handler"
	"github.com/Yangdongle668/Leyun/internal/api/middleware"
	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/web"
)

// NewRouter 构建完整的路由表。
func NewRouter(cfg *config.Config, svc *service.Registry) *gin.Engine {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()
	r.Use(middleware.Recovery(), middleware.AccessLog(), middleware.CORS(cfg.Server.AllowOrigins))
	r.RemoveExtraSlash = true

	h := handler.New(svc)
	authed := middleware.Auth(svc.DB, svc.JWT, svc.ACL)
	optional := middleware.OptionalAuth(svc.DB, svc.JWT, svc.ACL)
	pwdGuard := middleware.RequirePasswordChanged(cfg.Security.ForceChangeDefaultPassword)
	superAdmin := middleware.RequireSuperAdmin()
	anyAdmin := middleware.RequireAdmin()

	v1 := r.Group("/api/v1")

	// ---- 公开接口 ----
	v1.GET("/site", h.SiteInfo)
	v1.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	v1.POST("/auth/login", h.Login)
	// 乐云不提供自助注册：这些路径存在只是为了给出明确答复。
	v1.POST("/auth/register", middleware.NoRegister())
	v1.GET("/auth/register", middleware.NoRegister())

	// Document Server 用一次性令牌访问，不走登录态。
	v1.GET("/office/content", h.OfficeContent)
	v1.POST("/office/callback", h.OfficeCallback)

	// 分享：登录与否都可访问，登录态用于判定内部分享的可见范围。
	share := v1.Group("/share", optional)
	{
		share.GET("/:code/info", h.ShareInfo)
		share.GET("/:code/list", h.ShareList)
		share.GET("/:code/download", h.ShareDownload)
		share.GET("/:code/preview", h.SharePreview)
	}

	// ---- 登录后可访问（改密相关不受强制改密拦截）----
	account := v1.Group("/auth", authed)
	{
		account.GET("/profile", h.Profile)
		account.PUT("/profile", h.UpdateProfile)
		account.GET("/me", h.Me)
		account.POST("/logout", h.Logout)
		account.POST("/refresh", h.Refresh)
		account.POST("/password", h.ChangePassword)
	}

	app := v1.Group("", authed, pwdGuard)
	{
		app.GET("/spaces", h.ListSpaces)

		files := app.Group("/files")
		{
			files.GET("", h.ListFiles)
			files.POST("/folder", h.Mkdir)
			files.POST("/rename", h.Rename)
			files.POST("/move", h.MoveFiles)
			files.POST("/copy", h.CopyFiles)
			files.POST("/trash", h.TrashFiles)
			files.GET("/:id/download", h.DownloadFile)
			files.GET("/:id/preview", h.PreviewFile)
		}

		upload := app.Group("/upload")
		{
			upload.POST("", h.UploadFile)
			upload.POST("/init", h.InitUpload)
			upload.POST("/chunk", h.UploadChunk)
			upload.POST("/complete", h.CompleteUpload)
			upload.POST("/abort", h.AbortUpload)
		}

		trash := app.Group("/trash")
		{
			trash.GET("", h.ListTrash)
			trash.POST("/restore", h.RestoreFiles)
			trash.POST("/purge", h.PurgeFiles)
		}

		acl := app.Group("/acl")
		{
			acl.GET("", h.ListACL)
			acl.GET("/mine", h.MyPermissions)
			acl.POST("", h.Grant)
			acl.DELETE("/:id", h.Revoke)
		}

		shares := app.Group("/shares")
		{
			shares.GET("", h.ListShares)
			shares.POST("", h.CreateShare)
			shares.DELETE("/:id", h.RevokeShare)
		}

		app.GET("/office/config", h.OfficeConfig)

		// 选人/选部门在授权对话框里要用，普通成员也需要（但只返回基础信息）。
		app.GET("/directory/users", h.SearchUsers)
		app.GET("/directory/departments", h.DepartmentList)
	}

	// ---- 管理后台 ----
	admin := v1.Group("/admin", authed, pwdGuard)
	{
		// 只读页面部门管理员也能看。
		admin.GET("/overview", anyAdmin, h.Overview)
		admin.GET("/users", anyAdmin, h.ListUsers)
		admin.GET("/users/:id", anyAdmin, h.GetUser)
		admin.GET("/departments/tree", anyAdmin, h.DepartmentTree)
		admin.GET("/departments", anyAdmin, h.DepartmentList)
		admin.GET("/spaces", anyAdmin, h.ListAllSpaces)
		admin.GET("/audit-logs", anyAdmin, h.ListAuditLogs)
		admin.GET("/audit-actions", anyAdmin, h.AuditActions)

		// 开通账号、改部门、调配额、改系统设置——一律只限超级管理员。
		admin.POST("/users", superAdmin, h.CreateUser)
		admin.PUT("/users/:id", superAdmin, h.UpdateUser)
		admin.DELETE("/users/:id", superAdmin, h.DeleteUser)
		admin.POST("/users/:id/reset-password", superAdmin, h.ResetUserPassword)
		admin.POST("/departments", superAdmin, h.CreateDepartment)
		admin.PUT("/departments/:id", superAdmin, h.UpdateDepartment)
		admin.DELETE("/departments/:id", superAdmin, h.DeleteDepartment)
		admin.PUT("/spaces/:id", superAdmin, h.UpdateSpace)
		admin.GET("/settings", superAdmin, h.GetSettings)
		admin.PUT("/settings", superAdmin, h.UpdateSettings)
	}

	mountFrontend(r)
	return r
}

// mountFrontend 挂载前端单页应用。
//
// 除 /api 外的一切路径都要能回到 index.html，否则刷新 /admin/users 会 404。
func mountFrontend(r *gin.Engine) {
	assets := web.FileServer()
	index, _ := web.IndexHTML()

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 40400, "message": "接口不存在"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		// 带扩展名的请求交给静态文件服务；其余一律回 index.html 由前端路由接管。
		if hasStaticExt(path) {
			assets.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

func hasStaticExt(path string) bool {
	idx := strings.LastIndex(path, "/")
	last := path[idx+1:]
	dot := strings.LastIndex(last, ".")
	if dot <= 0 {
		return false
	}
	// index.html 也走静态服务，但 NoRoute 已经在上面处理过无扩展名的路由。
	return true
}
