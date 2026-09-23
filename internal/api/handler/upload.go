package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
)

// UploadFile 直传一个文件（适合小文件；大文件走分片接口）。
func (h *Handler) UploadFile(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	spaceID, err := strconv.ParseUint(c.PostForm("space_id"), 10, 64)
	if err != nil || spaceID == 0 {
		response.Fail(c, response.BadRequest("请指定空间"))
		return
	}
	parentID, _ := strconv.ParseUint(c.DefaultPostForm("parent_id", "0"), 10, 64)

	fh, err := c.FormFile("file")
	if err != nil {
		response.Fail(c, response.BadRequest("请选择要上传的文件"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.Fail(c, response.BadRequest("读取上传文件失败"))
		return
	}
	defer f.Close()

	name := fh.Filename
	if v := c.PostForm("filename"); v != "" {
		name = v
	}
	mode := service.ParseConflictMode(c.PostForm("conflict"))
	node, err := h.svc.Upload.SimpleUpload(subj, spaceID, parentID, name, fh.Size, f, mode)
	if err != nil {
		h.audit(c, service.ActionFileUpload, "node", 0, name, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileUpload, "node", node.ID, node.Name,
		service.HumanSize(node.Size), true)
	h.wakeIndexer()
	response.OK(c, node)
}

// InitUpload 初始化分片上传（可能直接命中秒传）。
func (h *Handler) InitUpload(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[service.InitInput](c)
	if !ok {
		return
	}
	res, err := h.svc.Upload.Init(subj, *req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	if res.Instant && res.Node != nil {
		h.audit(c, service.ActionFileUpload, "node", res.Node.ID, res.Node.Name, "秒传", true)
		// 秒传虽然没传字节，但空间里多了一份文件，一样要让它可被检索。
		h.wakeIndexer()
	}
	response.OK(c, res)
}

// UploadChunk 接收一个分片。
func (h *Handler) UploadChunk(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	uploadID := c.PostForm("upload_id")
	if uploadID == "" {
		uploadID = c.Query("upload_id")
	}
	if uploadID == "" {
		response.Fail(c, response.BadRequest("缺少上传会话号"))
		return
	}
	rawIndex := c.PostForm("index")
	if rawIndex == "" {
		rawIndex = c.Query("index")
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		response.Fail(c, response.BadRequest("缺少分片序号"))
		return
	}

	fh, err := c.FormFile("chunk")
	if err != nil {
		response.Fail(c, response.BadRequest("缺少分片数据"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		response.Fail(c, response.BadRequest("读取分片失败"))
		return
	}
	defer f.Close()

	received, err := h.svc.Upload.PutChunk(subj, uploadID, index, f)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"uploaded": received})
}

type completeUploadReq struct {
	UploadID string `json:"upload_id"`
}

// CompleteUpload 合并分片。
func (h *Handler) CompleteUpload(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[completeUploadReq](c)
	if !ok {
		return
	}
	node, err := h.svc.Upload.Complete(subj, req.UploadID)
	if err != nil {
		h.audit(c, service.ActionFileUpload, "node", 0, req.UploadID, err.Error(), false)
		response.Fail(c, err)
		return
	}
	h.audit(c, service.ActionFileUpload, "node", node.ID, node.Name,
		service.HumanSize(node.Size), true)
	h.wakeIndexer()
	response.OK(c, node)
}

// AbortUpload 放弃一次分片上传。
func (h *Handler) AbortUpload(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[completeUploadReq](c)
	if !ok {
		return
	}
	if err := h.svc.Upload.Abort(subj, req.UploadID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"ok": true})
}
