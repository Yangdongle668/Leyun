// Package web 把构建好的前端资源嵌进二进制，实现单文件部署。
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist 是 Vite 的构建产物目录。
//
// 仓库里只保留一个 .gitkeep（用 all: 前缀才会被 embed 收进来），
// 真正的前端资源由 `make web` 或 Docker 构建阶段生成。
//
//go:embed all:dist
var dist embed.FS

// placeholder 是前端尚未构建时的兜底页面，免得用户只看到一个 404。
const placeholder = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>乐云企业网盘</title>
<style>
  body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
       font-family:system-ui,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
       background:#f5f7fa;color:#1f2937}
  .card{background:#fff;padding:40px 48px;border-radius:16px;box-shadow:0 8px 32px rgba(15,23,42,.08);max-width:520px}
  h1{margin:0 0 12px;font-size:20px}
  p{margin:8px 0;line-height:1.7;color:#4b5563;font-size:14px}
  code{background:#f3f4f6;padding:2px 6px;border-radius:4px;font-size:13px}
</style>
</head>
<body>
  <div class="card">
    <h1>乐云企业网盘 · 后端已启动</h1>
    <p>当前二进制里没有前端资源。请先构建前端，再重新编译：</p>
    <p><code>make web &amp;&amp; make build</code></p>
    <p>或直接使用 <code>./deploy.sh</code> 一键部署（推荐）。</p>
    <p>接口可用性自检：<code>GET /api/v1/site</code></p>
  </div>
</body>
</html>`

// Assets 返回前端静态资源的文件系统；未构建时返回 nil。
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// IndexHTML 返回单页应用的入口文件内容。
func IndexHTML() ([]byte, bool) {
	assets := Assets()
	if assets == nil {
		return []byte(placeholder), false
	}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return []byte(placeholder), false
	}
	return data, true
}

// FileServer 返回静态资源处理器。
func FileServer() http.Handler {
	assets := Assets()
	if assets == nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(assets))
}
