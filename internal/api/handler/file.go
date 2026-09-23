package handler

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// ListSpaces 返回当前用户可见的空间。
func (h *Handler) ListSpaces(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	list, err := h.svc.Space.VisibleSpaces(subj)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ListFiles 列出目录内容。
func (h *Handler) ListFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	if spaceID == 0 {
		response.Fail(c, response.BadRequest("请指定空间"))
		return
	}
	res, err := h.svc.File.List(subj, spaceID, uintQuery(c, "parent_id", 0),
		strings.TrimSpace(c.Query("keyword")), c.Query("order_by"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, res)
}

type mkdirReq struct {
	SpaceID  uint64 `json:"space_id"`
	ParentID uint64 `json:"parent_id"`
	Name     string `json:"name"`
}

// Mkdir 新建目录。
func (h *Handler) Mkdir(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[mkdirReq](c)
	if !ok {
		return
	}
	node, err := h.svc.File.Mkdir(subj, req.SpaceID, req.ParentID, req.Name)
	if err != nil {
		h.audit(c, service.ActionFileMkdir, "node", 0, req.Name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileMkdir, "node", node.ID, node.Name, "", true)
	response.OK(c, node)
}

type renameReq struct {
	SpaceID uint64 `json:"space_id"`
	NodeID  uint64 `json:"node_id"`
	Name    string `json:"name"`
}

// Rename 重命名。
func (h *Handler) Rename(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[renameReq](c)
	if !ok {
		return
	}
	node, err := h.svc.File.Rename(subj, req.SpaceID, req.NodeID, req.Name)
	if err != nil {
		h.audit(c, service.ActionFileRename, "node", req.NodeID, req.Name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileRename, "node", node.ID, node.Name, "", true)
	response.OK(c, node)
}

// MoveFiles 移动文件。
func (h *Handler) MoveFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.MoveInput](c)
	if !ok {
		return
	}
	n, err := h.svc.File.Move(subj, *req)
	if err != nil {
		h.audit(c, service.ActionFileMove, "node", req.TargetID, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileMove, "node", req.TargetID, "",
		fmt.Sprintf("移动 %d 个条目", n), true)
	response.OK(c, gin.H{"moved": n})
}

// CopyFiles 复制文件。
func (h *Handler) CopyFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.MoveInput](c)
	if !ok {
		return
	}
	n, err := h.svc.File.Copy(subj, *req)
	if err != nil {
		h.audit(c, service.ActionFileCopy, "node", req.TargetID, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileCopy, "node", req.TargetID, "",
		fmt.Sprintf("复制 %d 个条目", n), true)
	response.OK(c, gin.H{"copied": n})
}

type nodeIDsReq struct {
	SpaceID uint64   `json:"space_id"`
	NodeIDs []uint64 `json:"node_ids"`
}

// TrashFiles 把文件移入回收站。
func (h *Handler) TrashFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[nodeIDsReq](c)
	if !ok {
		return
	}
	n, err := h.svc.File.Trash(subj, req.SpaceID, req.NodeIDs)
	if err != nil {
		h.audit(c, service.ActionFileTrash, "node", 0, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileTrash, "node", 0, "", fmt.Sprintf("删除 %d 个条目", n), true)
	response.OK(c, gin.H{"trashed": n})
}

// ListTrash 列出回收站。
func (h *Handler) ListTrash(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	list, total, err := h.svc.File.ListTrash(subj, uintQuery(c, "space_id", 0),
		intQuery(c, "page", 1), intQuery(c, "page_size", 20))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total, intQuery(c, "page", 1), intQuery(c, "page_size", 20))
}

// RestoreFiles 从回收站还原。
func (h *Handler) RestoreFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[nodeIDsReq](c)
	if !ok {
		return
	}
	n, err := h.svc.File.Restore(subj, req.NodeIDs)
	if err != nil {
		h.audit(c, service.ActionFileRestore, "node", 0, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileRestore, "node", 0, "", fmt.Sprintf("还原 %d 个条目", n), true)
	response.OK(c, gin.H{"restored": n})
}

// PurgeFiles 彻底删除回收站条目。
func (h *Handler) PurgeFiles(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[nodeIDsReq](c)
	if !ok {
		return
	}
	n, err := h.svc.File.Purge(subj, req.NodeIDs)
	if err != nil {
		h.audit(c, service.ActionFilePurge, "node", 0, "", err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFilePurge, "node", 0, "", fmt.Sprintf("彻底删除 %d 个条目", n), true)
	// 内容没了，索引里的向量也该跟着走。等两分钟轮询的话，
	// 这段时间里问答仍然可能引用到已经删掉的文件。
	h.wakeIndexer()
	response.OK(c, gin.H{"purged": n})
}

// DownloadFile 下载单个文件，支持断点续传。
func (h *Handler) DownloadFile(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	nodeID, ok := uintParam(c, "id")
	if !ok {
		return
	}
	space, err := h.svc.Space.Get(spaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(spaceID, nodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if node == nil {
		response.Fail(c, response.NotFound("文件不存在"))
		return
	}
	if _, err := h.svc.ACL.Require(subj, space, node, model.PermDownload); err != nil {
		response.Fail(c, err)
		return
	}
	if node.IsDir {
		h.streamDirectoryZip(c, node)
		return
	}

	h.audit(c, service.ActionFileDownload, "node", node.ID, node.Name, "", true)
	h.serveNode(c, node, true)
}

// PreviewFile 在线预览：图片、视频、PDF、纯文本等直接内联返回。
func (h *Handler) PreviewFile(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID := uintQuery(c, "space_id", 0)
	nodeID, ok := uintParam(c, "id")
	if !ok {
		return
	}
	space, err := h.svc.Space.Get(spaceID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	node, err := h.svc.File.GetNode(spaceID, nodeID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if node == nil || node.IsDir {
		response.Fail(c, response.NotFound("文件不存在"))
		return
	}
	// 预览只要求 view：这样"可看不可下"的设置才真正成立。
	if _, err := h.svc.ACL.Require(subj, space, node, model.PermView); err != nil {
		response.Fail(c, err)
		return
	}
	h.serveNode(c, node, false)
}

// serveNode 把文件内容写回响应。attachment 决定是下载还是内联预览。
func (h *Handler) serveNode(c *gin.Context, node *model.Node, attachment bool) {
	f, err := h.svc.File.OpenContent(node)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer f.Close()

	disposition := "inline"
	if attachment {
		disposition = "attachment"
	}
	// 中文文件名必须走 RFC 5987 的 filename*，否则在部分浏览器上会变成乱码。
	c.Header("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s",
		disposition, sanitizeASCII(node.Name), url.PathEscape(node.Name)))
	if node.MimeType != "" {
		c.Header("Content-Type", node.MimeType)
	}
	// 预览的 HTML/SVG 可能夹带脚本，统一禁掉嗅探并交给沙箱策略处理。
	c.Header("X-Content-Type-Options", "nosniff")
	if !attachment && isRiskyInline(node.Ext) {
		c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	c.Header("Accept-Ranges", "bytes")
	http.ServeContent(c.Writer, c.Request, node.Name, node.UpdatedAt, f)
}

// streamDirectoryZip 把一个目录打包成 zip 流式下载。
func (h *Handler) streamDirectoryZip(c *gin.Context, root *model.Node) {
	subj, _ := h.subject(c)
	nodes, err := h.svc.File.SubtreeFiles(root)
	if err != nil {
		response.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"; filename*=UTF-8''%s.zip",
		sanitizeASCII(root.Name), url.PathEscape(root.Name)))
	c.Header("Content-Type", "application/zip")
	c.Header("X-Content-Type-Options", "nosniff")

	zw := zip.NewWriter(c.Writer)
	defer func() {
		if err := zw.Close(); err != nil {
			logx.Warn("关闭 zip 失败", "node_id", root.ID, "err", err)
		}
	}()

	for _, item := range nodes {
		f, err := h.svc.File.OpenContent(&item.Node)
		if err != nil {
			logx.Warn("打包时跳过无法读取的文件", "node_id", item.ID, "err", err)
			continue
		}
		w, err := zw.Create(item.RelPath)
		if err != nil {
			f.Close()
			logx.Warn("写入 zip 条目失败", "path", item.RelPath, "err", err)
			continue
		}
		if _, err := io.Copy(w, f); err != nil {
			f.Close()
			// 连接断了就没必要继续打包了。
			logx.Warn("写入 zip 内容中断", "path", item.RelPath, "err", err)
			return
		}
		f.Close()
	}
	if subj != nil {
		h.audit(c, service.ActionFileDownload, "node", root.ID, root.Name, "打包下载目录", true)
	}
}

func sanitizeASCII(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if r < 32 || r == '"' || r == '\\' || r > 126 {
			sb.WriteByte('_')
			continue
		}
		sb.WriteRune(r)
	}
	out := sb.String()
	if out == "" {
		return "download"
	}
	return out
}

// isRiskyInline 判断内联预览时是否需要加沙箱。
func isRiskyInline(ext string) bool {
	switch strings.ToLower(ext) {
	case "html", "htm", "svg", "xml", "xhtml":
		return true
	}
	return false
}
