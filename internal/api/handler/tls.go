package handler

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// GetTLSSettings 读取域名与证书状态。
func (h *Handler) GetTLSSettings(c *gin.Context) {
	response.OK(c, gin.H{
		"status": h.svc.TLS.Status(),
		"raw":    h.svc.Setting.Get(store.SettingTLSDomains, ""),
	})
}

type tlsSettingsReq struct {
	Enabled *bool `json:"enabled"`
	// Domains 允许用逗号、空格或换行分隔，管理员怎么粘都认。
	Domains      *string `json:"domains"`
	Email        *string `json:"email"`
	DirectoryURL *string `json:"directory_url"`
	Redirect     *bool   `json:"redirect"`
	// AgreeTOS 必须显式勾选一次。ACME 协议要求明示同意签发机构的服务条款，
	// 替用户默认勾上是不合适的。
	AgreeTOS *bool `json:"agree_tos"`
}

// UpdateTLSSettings 保存域名配置。
func (h *Handler) UpdateTLSSettings(c *gin.Context) {
	req, ok := bind[tlsSettingsReq](c)
	if !ok {
		return
	}

	values := map[string]string{}
	if req.Domains != nil {
		domains := service.ParseDomains(*req.Domains)
		// 提前把申请不下来的域名挡住：IP、内网后缀这些等 ACME 报错回来，
		// 管理员要等几十秒才看到一句看不懂的英文。
		for _, d := range domains {
			if err := service.ValidateDomain(d); err != nil {
				response.Fail(c, response.BadRequest(err.Error()))
				return
			}
		}
		values[store.SettingTLSDomains] = strings.Join(domains, ",")
	}
	if req.Email != nil {
		email := strings.TrimSpace(*req.Email)
		if email != "" && (!strings.Contains(email, "@") || strings.HasPrefix(email, "@")) {
			response.Fail(c, response.BadRequest("联系邮箱格式不正确"))
			return
		}
		values[store.SettingTLSEmail] = email
	}
	if req.DirectoryURL != nil {
		values[store.SettingTLSDirectoryURL] = strings.TrimSpace(*req.DirectoryURL)
	}
	if req.Redirect != nil {
		values[store.SettingTLSRedirect] = boolText(*req.Redirect)
	}
	if req.AgreeTOS != nil {
		if *req.AgreeTOS {
			values[store.SettingTLSAgreedAt] = time.Now().Format(time.RFC3339)
		} else {
			values[store.SettingTLSAgreedAt] = ""
		}
	}
	if req.Enabled != nil {
		if *req.Enabled {
			// 开启前把前置条件查清楚，避免"开了却不生效"这种最难排查的状态。
			cfg := h.svc.TLS.Config()
			domains := cfg.Domains
			if v, ok := values[store.SettingTLSDomains]; ok {
				domains = service.ParseDomains(v)
			}
			agreed := cfg.AgreedAt != ""
			if v, ok := values[store.SettingTLSAgreedAt]; ok {
				agreed = v != ""
			}
			if len(domains) == 0 {
				response.Fail(c, response.BadRequest("请先填写要绑定的域名"))
				return
			}
			if !agreed {
				response.Fail(c, response.BadRequest("请勾选同意证书签发机构的服务条款"))
				return
			}
		}
		values[store.SettingTLSEnabled] = boolText(*req.Enabled)
	}

	if err := h.svc.Setting.SetMany(values); err != nil {
		response.Fail(c, response.BadRequest(err.Error()))
		return
	}
	h.audit(c, "tls.settings", "setting", 0, values[store.SettingTLSDomains], "", true)

	response.OK(c, gin.H{
		"status": h.svc.TLS.Status(),
		// 监听 443 要在进程启动时就建立，改完配置得重启一次才真正开始服务 HTTPS。
		// 与其让管理员纳闷"为什么还是 http"，不如直接说清楚。
		"restart_required": req.Enabled != nil && *req.Enabled,
		"restart_hint":     "已保存。首次启用需要重启服务（或重新执行 ./update.sh）才会开始监听 443 端口。",
	})
}

// IssueTLSCert 立刻申请或续期证书。
func (h *Handler) IssueTLSCert(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, 5*time.Minute)
	defer cancel()

	certs, err := h.svc.TLS.Issue(ctx)
	if err != nil {
		response.Fail(c, err)
		return
	}
	okCount := 0
	for _, ct := range certs {
		if ct.Issued {
			okCount++
		}
	}
	h.audit(c, "tls.issue", "cert", 0, "", itoaInt(okCount)+" 张成功", okCount > 0)
	response.OK(c, gin.H{"certs": certs, "status": h.svc.TLS.Status()})
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
