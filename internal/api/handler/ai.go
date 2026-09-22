package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/api/middleware"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// AIWhoami 让调用方自检这把密钥到底有什么能力。
//
// 接入时最常见的困惑是"我明明配了密钥怎么还是 403"，
// 有这个接口就能一眼看出是少了哪项 scope 还是空间被限死了。
func (h *Handler) AIWhoami(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	if key == nil {
		response.Fail(c, response.Unauthorized("缺少 API 密钥"))
		return
	}
	scopes := strings.Split(key.Key.Scopes, ",")
	response.OK(c, gin.H{
		"name":       key.Key.Name,
		"prefix":     key.Key.Prefix,
		"scopes":     scopes,
		"space_ids":  key.SpaceFilter(),
		"expire_at":  key.Key.ExpireAt,
		"created_at": key.Key.CreatedAt,
	})
}

// AISpaces 列出索引程序可抓取的空间。
func (h *Handler) AISpaces(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	list, err := h.svc.AI.ListSpaces(key.SpaceFilter())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// AIDocuments 枚举文档，支持全量与增量。
func (h *Handler) AIDocuments(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)

	q := service.DocumentQuery{
		Cursor:         c.Query("cursor"),
		Limit:          intQuery(c, "limit", 200),
		IncludeTrashed: boolQuery(c, "include_trashed", false),
		IncludeDirs:    boolQuery(c, "include_dirs", false),
	}
	// 请求方指定的空间必须落在密钥白名单内，否则直接拒绝而不是静默忽略——
	// 静默忽略会让调用方以为自己抓全了。
	requested := parseUint64List(c.Query("space_id"))
	if len(requested) > 0 {
		for _, id := range requested {
			if !key.AllowsSpace(id) {
				response.Fail(c, response.Forbidden("该密钥无权访问空间 "+strconv.FormatUint(id, 10)))
				return
			}
		}
		q.SpaceIDs = requested
	} else {
		q.SpaceIDs = key.SpaceFilter()
	}

	page, err := h.svc.AI.ListDocuments(q)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, page)
}

// AIDocument 读取单个文档的元数据。
func (h *Handler) AIDocument(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	item, node, err := h.svc.AI.GetDocument(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if !key.AllowsSpace(node.SpaceID) {
		response.Fail(c, response.Forbidden("该密钥无权访问此空间"))
		return
	}
	response.OK(c, item)
}

// AIDocumentContent 按节点取原始内容。
func (h *Handler) AIDocumentContent(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	_, node, err := h.svc.AI.GetDocument(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if !key.AllowsSpace(node.SpaceID) {
		response.Fail(c, response.Forbidden("该密钥无权访问此空间"))
		return
	}
	h.serveAIContent(c, node)
}

// AIBlobContent 按内容哈希取原始内容。
//
// 乐云是内容寻址存储：同一份文件被多个部门各存一份时哈希相同。
// 索引程序按哈希去重后，一份合同只需要解析一次，而不是按节点数重复解析。
func (h *Handler) AIBlobContent(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	hash := strings.ToLower(strings.TrimSpace(c.Param("hash")))
	node, err := h.svc.AI.FindNodeByBlob(hash)
	if err != nil {
		response.Fail(c, err)
		return
	}
	// 按哈希取内容同样要过空间白名单：否则限定了空间的密钥
	// 只要猜到哈希就能绕开限制拿到别的空间的文件。
	if !key.AllowsSpace(node.SpaceID) {
		// 这份内容可能被多个空间引用，换一个密钥够得着的引用再试。
		allowed := key.SpaceFilter()
		found := false
		if len(allowed) > 0 {
			var alt model.Node
			err := h.svc.DB.Where("blob_hash = ? AND is_dir = ? AND space_id IN ?", hash, false, allowed).
				First(&alt).Error
			if err == nil {
				node = &alt
				found = true
			}
		}
		if !found {
			response.Fail(c, response.Forbidden("该密钥无权访问此内容"))
			return
		}
	}
	h.serveAIContent(c, node)
}

func (h *Handler) serveAIContent(c *gin.Context, node *model.Node) {
	if node.IsDir {
		response.Fail(c, response.BadRequest("目录没有内容"))
		return
	}
	f, err := h.svc.File.OpenContent(node)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer f.Close()

	c.Header("Content-Type", node.MimeType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Leyun-Node-Id", strconv.FormatUint(node.ID, 10))
	c.Header("X-Leyun-Blob-Hash", node.BlobHash)
	// 内容寻址意味着哈希一变文件就是另一份，可以放心让调用方长期缓存。
	c.Header("ETag", `"`+node.BlobHash+`"`)
	http.ServeContent(c.Writer, c.Request, node.Name, node.UpdatedAt, f)
}

// AIDeletions 拉取彻底删除的流水，用于把外部索引里的幽灵数据清掉。
func (h *Handler) AIDeletions(c *gin.Context) {
	key := middleware.CurrentAPIKey(c)
	cursor := uintQuery(c, "cursor", 0)
	page, err := h.svc.AI.ListDeletions(cursor, intQuery(c, "limit", 200), key.SpaceFilter())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, page)
}

type aiAuthorizeReq struct {
	UserID uint64 `json:"user_id"`
	// Username 与 UserID 二选一，Agent 那边往往只拿得到登录名。
	Username string   `json:"username"`
	NodeIDs  []uint64 `json:"node_ids"`
	// Require 为空时按"能看见"判定；要判"能不能下载"就传 download。
	Require string `json:"require"`
}

// AIAuthorize 批量判定某个用户能看到哪些节点。
//
// 这是"按提问人权限过滤"的落点。知识库检索回一批候选片段后，
// 必须先用它把不该看的剔掉，再把剩下的内容送进模型。
func (h *Handler) AIAuthorize(c *gin.Context) {
	req, ok := bind[aiAuthorizeReq](c)
	if !ok {
		return
	}
	userID := req.UserID
	if userID == 0 && strings.TrimSpace(req.Username) != "" {
		brief, err := h.svc.AI.ResolveUser(req.Username)
		if err != nil {
			response.Fail(c, err)
			return
		}
		userID = brief.ID
	}
	if userID == 0 {
		response.Fail(c, response.BadRequest("请提供 user_id 或 username"))
		return
	}

	want := model.PermView
	if req.Require != "" {
		want = model.ParsePermissions([]string{req.Require})
		if want == model.PermNone {
			response.Fail(c, response.BadRequest("未知的权限项："+req.Require))
			return
		}
	}

	res, err := h.svc.AI.Authorize(userID, req.NodeIDs, want)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

// AIUser 读取用户概要，供 Agent 把 user_id 映射成人。
func (h *Handler) AIUser(c *gin.Context) {
	if raw := strings.TrimSpace(c.Query("username")); raw != "" {
		brief, err := h.svc.AI.ResolveUser(raw)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, brief)
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	brief, err := h.svc.AI.GetUser(id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, brief)
}

func parseUint64List(raw string) []uint64 {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		if id, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

// ===== 管理后台：密钥签发与吊销（走登录态，仅超级管理员） =====

// ListAPIKeys 列出全部密钥。
func (h *Handler) ListAPIKeys(c *gin.Context) {
	list, err := h.svc.APIKey.List()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"list": list, "scopes": service.AllScopes()})
}

// CreateAPIKey 签发密钥。明文只在这次响应里出现一次。
func (h *Handler) CreateAPIKey(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.CreateKeyInput](c)
	if !ok {
		return
	}
	created, err := h.svc.APIKey.Create(subj.User, *req)
	if err != nil {
		h.audit(c, "apikey.create", "apikey", 0, req.Name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, "apikey.create", "apikey", created.Record.ID, created.Record.Name,
		"能力="+created.Record.Scopes, true)
	response.OK(c, created)
}

type apiKeyStatusReq struct {
	Enabled bool `json:"enabled"`
}

// SetAPIKeyStatus 启用或停用密钥。
func (h *Handler) SetAPIKeyStatus(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	req, ok := bind[apiKeyStatusReq](c)
	if !ok {
		return
	}
	if err := h.svc.APIKey.SetEnabled(id, req.Enabled); err != nil {
		response.Fail(c, err)
		return
	}
	detail := "停用"
	if req.Enabled {
		detail = "启用"
	}
	h.audit(c, "apikey.status", "apikey", id, "", detail, true)
	response.OK(c, gin.H{"ok": true})
}

// DeleteAPIKey 删除密钥。
func (h *Handler) DeleteAPIKey(c *gin.Context) {
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.APIKey.Delete(id); err != nil {
		response.Fail(c, err)
		return
	}
	h.audit(c, "apikey.delete", "apikey", id, "", "", true)
	response.OK(c, gin.H{"ok": true})
}
