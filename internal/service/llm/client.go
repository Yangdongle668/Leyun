// Package llm 是一个 OpenAI 兼容的大模型客户端。
//
// 只认 OpenAI 那套接口（/v1/embeddings、/v1/chat/completions），
// 因为它已经是事实标准：通义千问、智谱、DeepSeek、Moonshot、豆包，
// 以及自建的 Ollama / Xinference / vLLM / one-api 全都提供兼容端点。
// 为每家单独写适配器没有意义，换供应商只要在后台改个地址和模型名。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 是一次调用需要的连接信息。
type Config struct {
	BaseURL    string
	APIKey     string
	ChatModel  string
	EmbedModel string
	Timeout    time.Duration
}

// Client 调用大模型服务。
type Client struct {
	cfg  Config
	http *http.Client
}

// New 构造客户端。
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{
		cfg: cfg,
		// 不共用 http.DefaultClient：它没有超时，模型服务卡住时
		// 索引协程会永远挂在那里，既不推进也不报错。
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// NormalizeBaseURL 把用户填的各种写法统一成带 /v1 的形式。
//
// 管理员填 "https://api.deepseek.com"、".../v1" 还是 ".../v1/" 都应该能用，
// 这种小事不该让人去翻文档。
func NormalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}
	// 已经指到具体端点的，退回到它的父路径。
	for _, suffix := range []string{"/chat/completions", "/embeddings"} {
		s = strings.TrimSuffix(s, suffix)
	}
	if !strings.HasSuffix(s, "/v1") {
		s += "/v1"
	}
	return s
}

func (c *Client) endpoint(path string) string {
	return NormalizeBaseURL(c.cfg.BaseURL) + path
}

// ===== 向量 =====

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *apiError `json:"error"`
}

// Embed 把一批文本转成向量。返回的顺序与入参一致。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.cfg.EmbedModel == "" {
		return nil, errors.New("未配置向量模型")
	}
	body, err := c.postJSON(ctx, c.endpoint("/embeddings"), embedReq{
		Model: c.cfg.EmbedModel,
		Input: texts,
	})
	if err != nil {
		return nil, err
	}
	var resp embedResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("向量接口返回的不是合法 JSON: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("向量接口报错: %s", resp.Error.Message)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("向量数量对不上：送了 %d 条，回来 %d 条", len(texts), len(resp.Data))
	}
	// 多数服务按顺序返回，但协议里带了 index，按它归位更稳妥——
	// 顺序错了会让每个片段配上别人的向量，检索结果会离奇地不相关，
	// 而且不会报任何错，极难排查。
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("向量接口返回了越界的下标 %d", d.Index)
		}
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("第 %d 条文本没有拿到向量", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("第 %d 条文本没有对应的向量", i)
		}
	}
	return out, nil
}

// ===== 对话 =====

// Message 是一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

type chatResp struct {
	Choices []struct {
		Message Message `json:"message"`
		Delta   Message `json:"delta"`
	} `json:"choices"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// ChatStream 发起一次流式对话，每拿到一小段就回调 onDelta。
//
// 做成流式是因为知识库问答的首字延迟很容易到好几秒（要先检索再生成），
// 一次性返回的话用户会以为页面卡死了。
func (c *Client) ChatStream(ctx context.Context, msgs []Message, temperature float32, onDelta func(string)) (string, error) {
	if c.cfg.ChatModel == "" {
		return "", errors.New("未配置对话模型")
	}
	payload, err := json.Marshal(chatReq{
		Model: c.cfg.ChatModel, Messages: msgs,
		Temperature: temperature, Stream: true,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	// 流式响应不能套客户端级超时：一次长回答本来就要几十秒，
	// 靠 ctx 控制整体时限即可。
	streamer := &http.Client{Timeout: 0}
	resp, err := streamer.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接模型服务失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeAPIError(resp)
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// 单条 SSE 数据可能很长，默认 64KB 缓冲不一定够。
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var frame chatResp
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			continue // 心跳或非标准帧，跳过即可
		}
		if frame.Error != nil {
			return sb.String(), fmt.Errorf("模型服务报错: %s", frame.Error.Message)
		}
		for _, ch := range frame.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			sb.WriteString(ch.Delta.Content)
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sb.String(), fmt.Errorf("读取模型响应中断: %w", err)
	}
	return sb.String(), nil
}

// Chat 非流式对话，用于内部短任务（比如给会话拟标题）。
func (c *Client) Chat(ctx context.Context, msgs []Message, temperature float32) (string, error) {
	if c.cfg.ChatModel == "" {
		return "", errors.New("未配置对话模型")
	}
	body, err := c.postJSON(ctx, c.endpoint("/chat/completions"), chatReq{
		Model: c.cfg.ChatModel, Messages: msgs, Temperature: temperature,
	})
	if err != nil {
		return "", err
	}
	var resp chatResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("对话接口返回的不是合法 JSON: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("对话接口报错: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("对话接口没有返回任何内容")
	}
	return resp.Choices[0].Message.Content, nil
}

// ===== 公共 =====

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
}

// postJSON 发一次请求，对限流与服务端错误做有限重试。
//
// 只重试 429 和 5xx：4xx 基本是模型名写错、密钥无效这类改了才会好的问题，
// 重试除了拖慢排查没有任何用处。
func (c *Client) postJSON(ctx context.Context, url string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	const maxAttempts = 3
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			delay := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("连接模型服务失败: %w", err)
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("读取模型响应失败: %w", readErr)
			continue
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return data, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = statusError(resp.StatusCode, data)
			continue
		default:
			return nil, statusError(resp.StatusCode, data)
		}
	}
	return nil, lastErr
}

func decodeAPIError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return statusError(resp.StatusCode, data)
}

// statusError 把服务端的错误体翻成一句人话。
func statusError(status int, body []byte) error {
	var wrapper struct {
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Error != nil && wrapper.Error.Message != "" {
		return fmt.Errorf("模型服务返回 %d：%s", status, wrapper.Error.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len([]rune(snippet)) > 200 {
		snippet = string([]rune(snippet)[:200]) + "…"
	}
	if snippet == "" {
		snippet = http.StatusText(status)
	}
	return fmt.Errorf("模型服务返回 %d：%s", status, snippet)
}
