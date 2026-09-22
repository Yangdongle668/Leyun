package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// TLSConfig 是域名与证书配置。
type TLSConfig struct {
	Enabled      bool
	Domains      []string
	Email        string
	DirectoryURL string
	Redirect     bool
	AgreedAt     string
}

// TLSService 负责绑定域名、自动申请与续期证书。
//
// 用 golang.org/x/crypto/acme/autocert 而不是自己实现 ACME：
// 它已经把申请、缓存、到期前自动续期都做好了，而且 x/crypto 本来就是依赖，
// 引它不增加任何新的外部依赖。
type TLSService struct {
	setting *SettingService
	cacheIn string

	mu      sync.RWMutex
	mgr     *autocert.Manager
	applied TLSConfig
	// lastErr 记录最近一次签发失败的原因，直接显示在管理页面上。
	// ACME 失败的原因五花八门（解析没生效、80 端口不通、频率超限），
	// 不摆出来管理员只能去翻日志。
	lastErr   string
	lastErrAt time.Time
}

// NewTLSService 构造服务。cacheDir 用来存证书与 ACME 账号密钥。
func NewTLSService(setting *SettingService, cacheDir string) *TLSService {
	return &TLSService{setting: setting, cacheIn: cacheDir}
}

// Config 读取当前配置。
func (s *TLSService) Config() TLSConfig {
	c := TLSConfig{
		Enabled:      s.setting.GetBool(store.SettingTLSEnabled, false),
		Email:        strings.TrimSpace(s.setting.Get(store.SettingTLSEmail, "")),
		DirectoryURL: strings.TrimSpace(s.setting.Get(store.SettingTLSDirectoryURL, "")),
		Redirect:     s.setting.GetBool(store.SettingTLSRedirect, true),
		AgreedAt:     s.setting.Get(store.SettingTLSAgreedAt, ""),
	}
	c.Domains = ParseDomains(s.setting.Get(store.SettingTLSDomains, ""))
	return c
}

// ParseDomains 把逗号/空格/换行分隔的域名串拆成规范化的列表。
func ParseDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == ';'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		d := normalizeDomain(f)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func normalizeDomain(raw string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	// 管理员很容易连 https:// 一起粘进来。
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimSuffix(strings.Split(d, "/")[0], ".")
	if h, _, err := net.SplitHostPort(d); err == nil {
		d = h
	}
	return d
}

// ValidateDomain 检查域名能不能拿去申请证书。
func ValidateDomain(d string) error {
	if d == "" {
		return errors.New("域名不能为空")
	}
	if len(d) > 253 {
		return fmt.Errorf("域名 %s 过长", d)
	}
	// 证书签发机构只认能公开解析的域名。IP、localhost、内网后缀一律申请不下来，
	// 与其让管理员等上几十秒再看一条看不懂的 ACME 报错，不如现在就说清楚。
	if net.ParseIP(d) != nil {
		return fmt.Errorf("%s 是 IP 地址，证书签发机构只给域名签发证书", d)
	}
	if !strings.Contains(d, ".") {
		return fmt.Errorf("%s 不是公网域名，无法申请证书", d)
	}
	for _, suffix := range []string{".local", ".internal", ".lan", ".localdomain", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(d, suffix) {
			return fmt.Errorf("%s 是内网域名，证书签发机构不会为它签发证书", d)
		}
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" {
			return fmt.Errorf("域名 %s 格式不正确", d)
		}
		if len(label) > 63 {
			return fmt.Errorf("域名 %s 中有过长的片段", d)
		}
		for _, r := range label {
			isOK := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '*' || r > 127
			if !isOK {
				return fmt.Errorf("域名 %s 含有非法字符", d)
			}
		}
	}
	return nil
}

// Manager 返回当前生效的 autocert 管理器；未启用时返回 nil。
//
// 配置变了会重建：管理员在后台加了个域名，不该还要去重启服务。
func (s *TLSService) Manager() *autocert.Manager {
	cfg := s.Config()
	if !cfg.Enabled || len(cfg.Domains) == 0 || cfg.AgreedAt == "" {
		s.mu.Lock()
		s.mgr, s.applied = nil, TLSConfig{}
		s.mu.Unlock()
		return nil
	}

	s.mu.RLock()
	if s.mgr != nil && sameTLSConfig(s.applied, cfg) {
		mgr := s.mgr
		s.mu.RUnlock()
		return mgr
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mgr != nil && sameTLSConfig(s.applied, cfg) {
		return s.mgr
	}

	allowed := make(map[string]bool, len(cfg.Domains))
	for _, d := range cfg.Domains {
		allowed[d] = true
	}
	mgr := &autocert.Manager{
		Cache:  autocert.DirCache(s.cacheIn),
		Prompt: autocert.AcceptTOS,
		Email:  cfg.Email,
		// HostPolicy 是最要紧的一道闸：不限制的话，任何人把自己的域名解析到
		// 这台机器，就能让它去替对方申请证书，很快会撞上签发机构的频率限额，
		// 把真正要用的域名也一起卡住。
		HostPolicy: func(_ context.Context, host string) error {
			if allowed[normalizeDomain(host)] {
				return nil
			}
			return fmt.Errorf("域名 %s 不在绑定列表里", host)
		},
	}
	if cfg.DirectoryURL != "" {
		mgr.Client = &acme.Client{DirectoryURL: cfg.DirectoryURL}
	}
	s.mgr, s.applied = mgr, cfg
	s.lastErr, s.lastErrAt = "", time.Time{}
	return mgr
}

func sameTLSConfig(a, b TLSConfig) bool {
	return a.Enabled == b.Enabled && a.Email == b.Email &&
		a.DirectoryURL == b.DirectoryURL && a.AgreedAt == b.AgreedAt &&
		strings.Join(a.Domains, ",") == strings.Join(b.Domains, ",")
}

// TLSConfigFor 返回给 http.Server 用的 tls.Config；未启用时返回 nil。
func (s *TLSService) TLSConfigFor() *tls.Config {
	mgr := s.Manager()
	if mgr == nil {
		return nil
	}
	cfg := mgr.TLSConfig()
	// 只留 TLS 1.2 以上：1.0/1.1 早已被各大浏览器标记为不安全，
	// 留着它们唯一的作用是让等保扫描报红。
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}

// noteError 记下最近一次失败，供界面展示。
func (s *TLSService) noteError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now()
	s.mu.Unlock()
}

// CertInfo 是一张证书的概况。
type CertInfo struct {
	Domain    string     `json:"domain"`
	Issued    bool       `json:"issued"`
	Issuer    string     `json:"issuer,omitempty"`
	NotBefore *time.Time `json:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`
	DaysLeft  int        `json:"days_left"`
	Err       string     `json:"err,omitempty"`
}

// TLSStatus 是给管理页面看的整体状态。
type TLSStatus struct {
	Enabled  bool       `json:"enabled"`
	Agreed   bool       `json:"agreed"`
	Redirect bool       `json:"redirect"`
	Domains  []string   `json:"domains"`
	Email    string     `json:"email"`
	Staging  bool       `json:"staging"`
	Certs    []CertInfo `json:"certs"`
	LastErr  string     `json:"last_err,omitempty"`
}

// Status 汇总当前证书情况。
func (s *TLSService) Status() *TLSStatus {
	cfg := s.Config()
	st := &TLSStatus{
		Enabled: cfg.Enabled, Agreed: cfg.AgreedAt != "", Redirect: cfg.Redirect,
		Domains: cfg.Domains, Email: cfg.Email,
		Staging: cfg.DirectoryURL != "",
	}
	s.mu.RLock()
	st.LastErr = s.lastErr
	s.mu.RUnlock()

	mgr := s.Manager()
	for _, d := range cfg.Domains {
		info := CertInfo{Domain: d}
		if mgr != nil {
			if cert, err := s.peekCert(mgr, d); err == nil && cert != nil {
				info.Issued = true
				info.Issuer = cert.Issuer.CommonName
				nb, na := cert.NotBefore, cert.NotAfter
				info.NotBefore, info.NotAfter = &nb, &na
				info.DaysLeft = int(time.Until(na).Hours() / 24)
			} else if err != nil {
				info.Err = err.Error()
			}
		}
		st.Certs = append(st.Certs, info)
	}
	return st
}

// peekCert 从缓存里读出已签发的证书，不触发签发。
//
// 用 GetCertificate 会在没有证书时当场去申请，而状态页是会被反复刷新的，
// 那样每刷一次都可能打一次签发机构，很容易撞上频率限额。
func (s *TLSService) peekCert(mgr *autocert.Manager, domain string) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := mgr.Cache.Get(ctx, domain)
	if err != nil {
		if errors.Is(err, autocert.ErrCacheMiss) {
			return nil, nil
		}
		return nil, err
	}
	// 缓存里是 PEM：先是私钥，后面跟着证书链。取链上第一张即叶子证书。
	for rest := data; len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return cert, nil
	}
	return nil, nil
}

// Issue 立刻为绑定的域名申请（或续期）证书。
//
// autocert 平时是"请求进来时按需签发"，但管理员点了保存之后
// 总希望马上看到结果，而不是等第一个访客来触发。
func (s *TLSService) Issue(ctx context.Context) ([]CertInfo, error) {
	cfg := s.Config()
	if !cfg.Enabled {
		return nil, response.BadRequest("尚未启用 HTTPS")
	}
	if cfg.AgreedAt == "" {
		return nil, response.BadRequest("请先勾选同意证书签发机构的服务条款")
	}
	mgr := s.Manager()
	if mgr == nil {
		return nil, response.BadRequest("请先填写要绑定的域名")
	}

	out := make([]CertInfo, 0, len(cfg.Domains))
	for _, d := range cfg.Domains {
		info := CertInfo{Domain: d}
		cert, err := issueWithTimeout(ctx, mgr, d, 90*time.Second)
		if err != nil {
			info.Err = friendlyACMEError(d, err)
			s.noteError(fmt.Errorf("%s: %s", d, info.Err))
		} else if cert != nil && len(cert.Certificate) > 0 {
			if parsed, perr := x509.ParseCertificate(cert.Certificate[0]); perr == nil {
				info.Issued = true
				info.Issuer = parsed.Issuer.CommonName
				nb, na := parsed.NotBefore, parsed.NotAfter
				info.NotBefore, info.NotAfter = &nb, &na
				info.DaysLeft = int(time.Until(na).Hours() / 24)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// issueWithTimeout 申请一张证书，并给它一个上限。
//
// autocert.GetCertificate 不收 context，而 ACME 验证在解析没生效、
// 端口不通时会拖很久。管理页面等着它返回，不设上限就是一个挂死的请求。
func issueWithTimeout(ctx context.Context, mgr *autocert.Manager, domain string, d time.Duration) (*tls.Certificate, error) {
	type result struct {
		cert *tls.Certificate
		err  error
	}
	// 缓冲 1：超时返回后这个协程还在跑，没有接收方也不能让它卡住。
	// 它最终仍会把证书写进缓存，下次点续期就直接命中了。
	ch := make(chan result, 1)
	go func() {
		cert, err := mgr.GetCertificate(&tls.ClientHelloInfo{
			ServerName: domain,
			// autocert 按支持的协议挑验证方式，不声明就走不了 TLS-ALPN-01。
			SupportedProtos: []string{acme.ALPNProto},
		})
		ch <- result{cert, err}
	}()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.cert, r.err
	case <-timer.C:
		return nil, fmt.Errorf("等待 %s 超时，验证多半没能完成", d)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// friendlyACMEError 把 ACME 的报错翻成管理员能照着动手的一句话。
//
// 原始错误多半是一长串英文外加一个 URL，对着它没人知道该去改什么。
func friendlyACMEError(domain string, err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "no such host"), strings.Contains(low, "dns problem"),
		strings.Contains(low, "nxdomain"):
		return fmt.Sprintf("%s 的 DNS 解析没查到。请先把它解析到本机公网 IP，等解析生效后再试。（原始信息：%s）", domain, msg)
	case strings.Contains(low, "connection refused"), strings.Contains(low, "timeout"),
		strings.Contains(low, "unauthorized"), strings.Contains(low, "fetching http"):
		return fmt.Sprintf("签发机构访问不到本机。请确认 80 与 443 端口在防火墙和安全组里都对公网开放，且没有被其它程序占用。（原始信息：%s）", msg)
	case strings.Contains(low, "too many"), strings.Contains(low, "rate limit"):
		return fmt.Sprintf("触发了签发机构的频率限额，请等一小时后再试；调试阶段建议先填测试环境地址。（原始信息：%s）", msg)
	default:
		return msg
	}
}
