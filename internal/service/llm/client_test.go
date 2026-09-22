package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeBaseURL(t *testing.T) {
	// 管理员填哪种写法都得能用，这种小事不该逼人去翻文档。
	cases := map[string]string{
		"https://api.deepseek.com":                     "https://api.deepseek.com/v1",
		"https://api.deepseek.com/":                    "https://api.deepseek.com/v1",
		"https://api.deepseek.com/v1":                  "https://api.deepseek.com/v1",
		"https://api.deepseek.com/v1/":                 "https://api.deepseek.com/v1",
		"  https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1",
		"http://127.0.0.1:11434/v1/embeddings":         "http://127.0.0.1:11434/v1",
		"http://ollama:11434":                          "http://ollama:11434/v1",
		"": "",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEmbedReordersByIndex 向量必须按协议里的 index 归位。
//
// 这是最阴险的一类 bug：服务端乱序返回时，如果按数组顺序硬配，
// 每个片段会配上别人的向量。检索结果会离奇地不相关，但不报任何错。
func TestEmbedReordersByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("密钥没带上: %q", got)
		}
		var req embedReq
		json.NewDecoder(r.Body).Decode(&req)
		// 故意倒序返回
		out := embedResp{}
		for i := len(req.Input) - 1; i >= 0; i-- {
			out.Data = append(out.Data, struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}{Index: i, Embedding: []float32{float32(i), float32(i) + 0.5}})
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "sk-test", EmbedModel: "bge-m3"})
	got, err := c.Embed(context.Background(), []string{"甲", "乙", "丙"})
	if err != nil {
		t.Fatalf("Embed 失败: %v", err)
	}
	for i, v := range got {
		if v[0] != float32(i) {
			t.Fatalf("第 %d 条配错了向量: %v", i, v)
		}
	}
}

func TestEmbedRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只回一条，少了两条
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[0.1,0.2]}]}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, EmbedModel: "bge-m3"})
	if _, err := c.Embed(context.Background(), []string{"甲", "乙", "丙"}); err == nil {
		t.Fatal("数量对不上却没报错——静默丢向量会让索引出现无声的空洞")
	}
}

func TestEmbedSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, EmbedModel: "bge-m3"})
	_, err := c.Embed(context.Background(), []string{"甲"})
	if err == nil {
		t.Fatal("401 竟然没报错")
	}
	// 要把服务端那句话原样带出来，管理员才知道是密钥错了而不是网络不通。
	if !strings.Contains(err.Error(), "Incorrect API key") {
		t.Fatalf("没把服务端的错误信息带出来: %v", err)
	}
}

// TestRetryOnlyOnTransient 只对 429 和 5xx 重试。
//
// 4xx 基本是模型名写错、密钥无效这类改了才会好的问题，
// 重试除了拖慢排查没有任何用处。
func TestRetryOnlyOnTransient(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,2]}]}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, EmbedModel: "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := c.Embed(ctx, []string{"甲"}); err != nil {
		t.Fatalf("限流后应当重试成功: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("重试次数不对: %d", got)
	}

	calls.Store(0)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"model not found"}}`)
	}))
	defer bad.Close()

	c2 := New(Config{BaseURL: bad.URL, EmbedModel: "nope"})
	if _, err := c2.Embed(context.Background(), []string{"甲"}); err == nil {
		t.Fatal("400 竟然成功了")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("4xx 不该重试，实际请求了 %d 次", got)
	}
}

// TestChatStreamAssembles 流式返回要能按帧拼成完整答案。
func TestChatStreamAssembles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"choices":[{"delta":{"content":"按照"}}]}`,
			`: 这是一条心跳，要被忽略`,
			``,
			`{"choices":[{"delta":{"content":"权限说明书"}}]}`,
			`{"choices":[{"delta":{"content":"，拒绝优先。"}}]}`,
			`[DONE]`,
		}
		for _, f := range frames {
			if strings.HasPrefix(f, ":") || f == "" {
				fmt.Fprintf(w, "%s\n\n", f)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", f)
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, ChatModel: "qwen-max"})
	var deltas []string
	got, err := c.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "拒绝和允许谁优先？"}}, 0.2,
		func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	const want = "按照权限说明书，拒绝优先。"
	if got != want {
		t.Fatalf("拼接结果不对:\n want %q\n got  %q", want, got)
	}
	if len(deltas) != 3 {
		t.Fatalf("回调次数不对: %d (%v)", len(deltas), deltas)
	}
}

func TestChatStreamSurfacesMidStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"开头\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"context length exceeded\"}}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, ChatModel: "m"})
	_, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}}, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "context length") {
		t.Fatalf("流中途的错误没有被报出来: %v", err)
	}
}

func TestMissingModelIsRejectedEarly(t *testing.T) {
	c := New(Config{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.Embed(context.Background(), []string{"甲"}); err == nil {
		t.Fatal("没配向量模型就该直接报错，不该去连服务")
	}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}}, 0); err == nil {
		t.Fatal("没配对话模型就该直接报错")
	}
}
