package handler

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Yangdongle668/Leyun/internal/service"
)

// OfficeRootPaths 是 Document Server 在**根路径**上占用的地址。
//
// 为什么根上也要接一份——
//
// DS 在响应里给出的是绝对地址，而且是按根路径拼的，不带 /onlyoffice 前缀。
// 最典型的是转换后的文件：
//
//	GET https://域名/cache/files/data/n2-v1-.../origin.pdf  →  404
//
// 浏览器照着去取，落到乐云的前端兜底上，得到 404。表现就是"编辑器能打开，
// 一动手编辑就报错"。把这些路径也转给 DS，它给什么地址都能落到实处。
//
// 这些前缀和乐云自己的路由没有重叠，只有 /fonts 是例外，见 LeyunFontPrefix。
var OfficeRootPaths = []string{
	"/cache", "/web-apps", "/sdkjs", "/sdkjs-plugins", "/dictionaries",
	"/coauthoring", "/doc", "/downloadfile", "/internal", "/info",
	"/hosting", "/converter", "/fonts",
}

// LeyunFontPrefix 是乐云自己打包的中文字体，和 DS 的 /fonts 撞在一起了。
// 这一段必须留给乐云，其余 /fonts/* 才转给 DS——否则界面会退回宋体。
const LeyunFontPrefix = "/fonts/noto-sans-sc/"

// OfficeProxy 把 /onlyoffice/* 原样转发给 Document Server。
//
// 为什么非要在自己这里转一道——
//
// 编辑器的 JS、字体、图标和协同用的 websocket 全都是浏览器直接去拿的。
// 以前下发的是 Document Server 的对外地址（形如 http://1.2.3.4:8081）。
// 一旦网盘绑了域名走 https，浏览器就会按混合内容规则把这些请求全部拦掉：
//
//	Mixed Content: The page at 'https://例子.com/office/4/5' was loaded over
//	HTTPS, but requested an insecure script 'http://1.2.3.4:8081/...'.
//	This request has been blocked.
//
// 在线编辑于是彻底打不开，而且这是必然的——不是偶发。
//
// 转发之后浏览器只跟乐云这一个源打交道：页面是 http 就走 http，是 https
// 就走 https，永远不会错配，证书也只要一张，8081 更不必暴露到公网。
// 代价是流量多走一跳，但编辑器那几 MB 静态资源浏览器会缓存，实际开销很小。
func (h *Handler) OfficeProxy() gin.HandlerFunc {
	raw := strings.TrimSpace(h.svc.Cfg.Office.InternalURL)
	target, err := url.Parse(raw)
	if err != nil || target.Scheme == "" || target.Host == "" {
		// 没配内网地址就没法转发。这里不能直接 panic：Office 功能本来就是
		// 可选的，没部署 Document Server 的实例要照常启动。
		return func(c *gin.Context) {
			c.String(http.StatusServiceUnavailable,
				"未配置 Document Server 的内网地址（office.internal_url），无法转发")
		}
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// 前面那层如果已经表明了协议（我们跑在 Nginx 之类后面时），
			// 要保住它：SetXForwarded 只看 r.In.TLS，那种情况下是 nil，
			// 会把 https 误写成 http，Document Server 据此拼出来的绝对地址
			// 就又变成 http 了，等于白转。
			proto := r.In.Header.Get("X-Forwarded-Proto")
			host := r.In.Header.Get("X-Forwarded-Host")
			r.SetXForwarded()
			if proto != "" {
				r.Out.Header.Set("X-Forwarded-Proto", proto)
			}
			if host != "" {
				r.Out.Header.Set("X-Forwarded-Host", host)
			}

			// 去掉 /onlyoffice 前缀。Document Server 认为自己跑在根路径上，
			// 带着前缀转过去它一律 404。
			p := strings.TrimPrefix(r.In.URL.Path, service.OfficeProxyPath)
			if p == "" || p[0] != '/' {
				p = "/" + p
			}
			r.Out.URL.Scheme = target.Scheme
			r.Out.URL.Host = target.Host
			r.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + p
			r.Out.URL.RawQuery = r.In.URL.RawQuery
			// Host 按目标服务写，Document Server 用它判断自己是谁。
			r.Out.Host = target.Host
		},
		// 立刻转发每一段响应，不攒缓冲。协同编辑那条长连接攒起来就是卡住。
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "连接 Document Server 失败：%v", err)
		},
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
