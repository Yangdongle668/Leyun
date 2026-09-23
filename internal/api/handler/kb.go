package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/service/extract"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// ===== 用户侧：问答 =====

type kbAskReq struct {
	ConvID   uint64 `json:"conv_id"`
	Question string `json:"question"`
}

// KBAsk 提问，用 SSE 流式返回。
//
// 做成流式是因为知识库问答的首字延迟本来就长（要先向量化提问、检索、过权限、
// 再等模型开口），一次性返回的话用户会以为页面卡死了。
func (h *Handler) KBAsk(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	req, ok := bind[kbAskReq](c)
	if !ok {
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	// 反向代理默认会缓冲响应，缓冲住就没有"流式"可言了。
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, canFlush := c.Writer.(http.Flusher)
	emit := func(ev service.ChatEvent) {
		payload, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		if canFlush {
			flusher.Flush()
		}
	}

	// 用请求的 ctx：用户关掉页面时连接断开，ctx 被取消，
	// 正在跑的模型调用也跟着停，不会白白烧额度。
	res, err := h.svc.KB.Answer(c.Request.Context(), subj, service.AnswerInput{
		ConvID: req.ConvID, Question: req.Question,
	}, emit)
	if err != nil {
		emit(service.ChatEvent{Type: "error", Data: userMessage(err)})
		h.audit(c, "kb.ask", "kb", req.ConvID, clip(req.Question, 80), err.Error(), false)
		return
	}
	emit(service.ChatEvent{Type: "done", Data: gin.H{
		"conv_id": res.ConvID, "answer": res.Answer,
	}})
	h.audit(c, "kb.ask", "kb", res.ConvID, clip(req.Question, 80),
		fmt.Sprintf("引用 %d 条", len(res.Citations)), true)
}

// KBStatusForUser 告诉前端问答入口该不该显示。
func (h *Handler) KBStatusForUser(c *gin.Context) {
	cfg := h.svc.KB.Config()
	response.OK(c, gin.H{
		"enabled":  cfg.Enabled,
		"can_chat": cfg.CanChat(),
	})
}

// KBConversations 列出我的会话。
func (h *Handler) KBConversations(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	list, err := h.svc.KB.Conversations(subj.User.ID, intQuery(c, "limit", 30))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// KBConversationDetail 读取某个会话的消息。
func (h *Handler) KBConversationDetail(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	msgs, err := h.svc.KB.ConversationMessages(subj.User.ID, id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, msgs)
}

// KBDeleteConversation 删除会话。
func (h *Handler) KBDeleteConversation(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	id, ok := uintParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.KB.DeleteConversation(subj.User.ID, id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// ===== 管理侧：配置与索引 =====

// AIConfigView 是回给管理页面的配置。API Key 只给掩码。
type AIConfigView struct {
	Enabled         bool     `json:"enabled"`
	BaseURL         string   `json:"base_url"`
	APIKeyMask      string   `json:"api_key_mask"`
	APIKeySet       bool     `json:"api_key_set"`
	ChatModel       string   `json:"chat_model"`
	EmbedModel      string   `json:"embed_model"`
	EmbedDim        int      `json:"embed_dim"`
	EmbedBatch      int      `json:"embed_batch"`
	ChunkSize       int      `json:"chunk_size"`
	ChunkOverlap    int      `json:"chunk_overlap"`
	TopK            int      `json:"top_k"`
	SpaceIDs        []uint64 `json:"space_ids"`
	IncludePersonal bool     `json:"include_personal"`
	MaxFileSize     int64    `json:"max_file_size"`
}

func (h *Handler) aiConfigView() AIConfigView {
	cfg := h.svc.KB.Config()
	return AIConfigView{
		Enabled: cfg.Enabled, BaseURL: cfg.BaseURL,
		APIKeyMask: service.MaskSecret(cfg.APIKey), APIKeySet: cfg.APIKey != "",
		ChatModel: cfg.ChatModel, EmbedModel: cfg.EmbedModel, EmbedDim: cfg.EmbedDim,
		EmbedBatch: cfg.EmbedBatch,
		ChunkSize:  cfg.ChunkSize, ChunkOverlap: cfg.ChunkOverlap, TopK: cfg.TopK,
		SpaceIDs: cfg.SpaceIDs, IncludePersonal: cfg.IncludePersonal,
		MaxFileSize: cfg.MaxFileSize,
	}
}

// GetAISettings 读取知识库配置与索引状态。
func (h *Handler) GetAISettings(c *gin.Context) {
	status, err := h.svc.KB.Status()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{
		"config":     h.aiConfigView(),
		"status":     status,
		"extensions": extract.Extensions(),
	})
}

type aiSettingsReq struct {
	Enabled *bool   `json:"enabled"`
	BaseURL *string `json:"base_url"`
	// APIKey 留空表示不改；传 "-" 表示清空。
	// 不能用空串表示清空：管理页面提交整张表单时空着的字段也会带上来，
	// 那样改个模型名就会把密钥抹掉。
	APIKey          *string   `json:"api_key"`
	ChatModel       *string   `json:"chat_model"`
	EmbedModel      *string   `json:"embed_model"`
	EmbedDim        *int      `json:"embed_dim"`
	EmbedBatch      *int      `json:"embed_batch"`
	ChunkSize       *int      `json:"chunk_size"`
	ChunkOverlap    *int      `json:"chunk_overlap"`
	TopK            *int      `json:"top_k"`
	SpaceIDs        *[]uint64 `json:"space_ids"`
	IncludePersonal *bool     `json:"include_personal"`
	MaxFileSize     *int64    `json:"max_file_size"`
}

// UpdateAISettings 保存知识库配置。
func (h *Handler) UpdateAISettings(c *gin.Context) {
	req, ok := bind[aiSettingsReq](c)
	if !ok {
		return
	}
	before := h.svc.KB.Config()

	values := map[string]string{}
	if req.Enabled != nil {
		values[store.SettingAIEnabled] = strconv.FormatBool(*req.Enabled)
	}
	if req.BaseURL != nil {
		values[store.SettingAIBaseURL] = strings.TrimSpace(*req.BaseURL)
	}
	if req.APIKey != nil {
		switch v := strings.TrimSpace(*req.APIKey); v {
		case "":
			// 不动
		case "-":
			values[store.SettingAIAPIKey] = ""
		default:
			values[store.SettingAIAPIKey] = v
		}
	}
	if req.ChatModel != nil {
		values[store.SettingAIChatModel] = strings.TrimSpace(*req.ChatModel)
	}
	if req.EmbedModel != nil {
		values[store.SettingAIEmbedModel] = strings.TrimSpace(*req.EmbedModel)
	}
	if req.EmbedDim != nil {
		values[store.SettingAIEmbedDim] = strconv.Itoa(*req.EmbedDim)
	}
	if req.EmbedBatch != nil {
		// 负数当成"自动"，省得存进去反而把批量算成 0 条。
		values[store.SettingAIEmbedBatch] = strconv.Itoa(max(0, *req.EmbedBatch))
	}
	if req.ChunkSize != nil {
		values[store.SettingAIChunkSize] = strconv.Itoa(*req.ChunkSize)
	}
	if req.ChunkOverlap != nil {
		values[store.SettingAIChunkOverlap] = strconv.Itoa(*req.ChunkOverlap)
	}
	if req.TopK != nil {
		values[store.SettingAITopK] = strconv.Itoa(*req.TopK)
	}
	if req.SpaceIDs != nil {
		parts := make([]string, 0, len(*req.SpaceIDs))
		for _, id := range *req.SpaceIDs {
			parts = append(parts, strconv.FormatUint(id, 10))
		}
		values[store.SettingAISpaceIDs] = strings.Join(parts, ",")
	}
	if req.IncludePersonal != nil {
		values[store.SettingAIIncludePersonal] = strconv.FormatBool(*req.IncludePersonal)
	}
	if req.MaxFileSize != nil {
		values[store.SettingAIMaxFileSize] = strconv.FormatInt(*req.MaxFileSize, 10)
	}

	if err := h.svc.Setting.SetMany(values); err != nil {
		response.Fail(c, response.BadRequest(err.Error()))
		return
	}
	h.audit(c, "ai.settings", "setting", 0, "知识库配置", "", true)

	// 换了向量模型或维度，旧向量与新查询不在同一个语义空间里，
	// 比出来的相似度没有意义，必须整体重建。这里只提示，不自动删——
	// 重建要重新烧一遍向量额度，得让管理员自己决定什么时候做。
	after := h.svc.KB.Config()
	needReindex := after.EmbedModel != before.EmbedModel || after.EmbedDim != before.EmbedDim

	h.svc.KB.Wake()
	response.OK(c, gin.H{
		"config":       h.aiConfigView(),
		"need_reindex": needReindex,
		"reindex_hint": "向量模型或维度已变化，需要重建索引后检索才准确。",
	})
}

// TestAIConnection 试连大模型服务。
func (h *Handler) TestAIConnection(c *gin.Context) {
	req, ok := bind[aiSettingsReq](c)
	if !ok {
		return
	}
	cfg := h.svc.KB.Config()
	// 允许用页面上还没保存的值试连，省得"先存错的再试"。
	if req.BaseURL != nil {
		cfg.BaseURL = strings.TrimSpace(*req.BaseURL)
	}
	if req.APIKey != nil {
		if v := strings.TrimSpace(*req.APIKey); v != "" && v != "-" {
			cfg.APIKey = v
		}
	}
	if req.ChatModel != nil {
		cfg.ChatModel = strings.TrimSpace(*req.ChatModel)
	}
	if req.EmbedModel != nil {
		cfg.EmbedModel = strings.TrimSpace(*req.EmbedModel)
	}
	if req.EmbedDim != nil {
		cfg.EmbedDim = *req.EmbedDim
	}

	ctx, cancel := contextWithTimeout(c, 45*time.Second)
	defer cancel()
	out, err := h.svc.KB.TestConnection(ctx, cfg)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, out)
}

// KBAdminStatus 索引状态。
func (h *Handler) KBAdminStatus(c *gin.Context) {
	status, err := h.svc.KB.Status()
	if err != nil {
		response.Fail(c, err)
		return
	}
	failed, err := h.svc.KB.FailedDocs(intQuery(c, "limit", 50))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"status": status, "problems": failed})
}

// KBReindex 清空并重建索引。
func (h *Handler) KBReindex(c *gin.Context) {
	if err := h.svc.KB.Reindex(); err != nil {
		response.Fail(c, err)
		return
	}
	h.audit(c, "kb.reindex", "kb", 0, "", "重建全部索引", true)
	response.OK(c, gin.H{"ok": true})
}

// KBRetryFailed 把失败的文档放回队列。
func (h *Handler) KBRetryFailed(c *gin.Context) {
	n, err := h.svc.KB.RetryFailed()
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.audit(c, "kb.retry", "kb", 0, "", fmt.Sprintf("重试 %d 份", n), true)
	response.OK(c, gin.H{"retried": n})
}

// KBSearchPreview 让管理员用某个身份试检索，验证权限过滤是否符合预期。
//
// 只返回命中的文件名与得分，不返回正文——这是个诊断工具，
// 不该变成"管理员用别人的身份看内容"的后门。
func (h *Handler) KBSearchPreview(c *gin.Context) {
	subj, ok := h.subject(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		response.Fail(c, response.BadRequest("请输入检索词"))
		return
	}
	passages, err := h.svc.KB.Retrieve(subj, query, intQuery(c, "limit", 10))
	if err != nil {
		response.Fail(c, err)
		return
	}
	out := make([]gin.H, 0, len(passages))
	for _, p := range passages {
		out = append(out, gin.H{
			"node_id": p.NodeID, "name": p.Name,
			"path_names": p.PathNames, "score": p.Score,
			"preview": clip(p.Text, 120),
		})
	}
	response.OK(c, out)
}

func clip(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}

// userMessage 把错误翻成能直接给用户看的一句话。
//
// SSE 已经把状态码写成 200 了，错误只能放在帧里传，
// 所以这里要给出人话而不是内部错误串。
func userMessage(err error) string {
	var re *response.Error
	if errors.As(err, &re) {
		return re.Message
	}
	return err.Error()
}

// contextWithTimeout 在请求上下文之上再加一层超时。
func contextWithTimeout(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}
