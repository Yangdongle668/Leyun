#!/usr/bin/env bash
#
# 重新抓取 Noto Sans SC 并生成 src/styles/fonts.css
#
# 平时不需要跑：字体文件已经提交进仓库，构建时直接用。
# 只有要换字重、换字体，或 Google 那边更新了版本时才需要重跑。
#
#   cd web && ./scripts/fetch-fonts.sh
#
# 为什么要把字体打包进来而不是用 CDN：
# 乐云是私有化部署，内网没有外网是常态。从 fonts.googleapis.com 拉字体
# 在内网必然失败，中文会退化成宋体；国内即便有外网也访问不到。
#
# 许可：Noto Sans SC 采用 SIL Open Font License 1.1，允许随产品分发。

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly WEIGHTS="400;500;600"
readonly OUT_DIR="public/fonts/noto-sans-sc"
readonly CSS_OUT="src/styles/fonts.css"
# 必须带桌面浏览器 UA：Google 会按 UA 决定给 woff2 还是老格式
readonly UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120 Safari/537.36"

echo "▸ 拉取 CSS（字重 ${WEIGHTS}）"
tmp_css="$(mktemp)"
trap 'rm -f "$tmp_css"' EXIT
curl -fsS -A "$UA" \
  "https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@${WEIGHTS}&display=swap" \
  -o "$tmp_css"

mkdir -p "$OUT_DIR"
rm -f "$OUT_DIR"/*.woff2

CSS_IN="$tmp_css" OUT_DIR="$OUT_DIR" CSS_OUT="$CSS_OUT" python3 - <<'PY'
import os, re, urllib.request

css = open(os.environ['CSS_IN'], encoding='utf-8').read()
out_dir, css_out = os.environ['OUT_DIR'], os.environ['CSS_OUT']

urls = sorted(set(re.findall(r'https://[^)]+\.woff2', css)))
print(f"▸ 下载 {len(urls)} 个分片")

for i, url in enumerate(urls, 1):
    # 直接用 URL 的 basename 落盘：不做任何改名，杜绝解析出错
    name = url.rsplit('/', 1)[-1]
    req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
    data = urllib.request.urlopen(req, timeout=60).read()
    open(os.path.join(out_dir, name), 'wb').write(data)
    css = css.replace(url, f'/fonts/noto-sans-sc/{name}')
    if i % 20 == 0:
        print(f"  {i}/{len(urls)}")

assert 'fonts.gstatic' not in css, "仍有外链没被替换掉"

header = '''/*
 * Noto Sans SC —— 随程序打包，不走 CDN。
 *
 * 乐云是私有化部署，内网无外网是常态；从 Google Fonts 拉字体在内网
 * 必然失败、退化成宋体，国内也访问不到。所以字体文件全部落在本地，
 * 由 Go 的 embed 一并打进二进制。
 *
 * 下面这几百个 @font-face 指向的其实只有一百来个文件（可变字体，
 * 一个文件覆盖全部字重），并且按 unicode-range 切成了小块：
 * 浏览器只会下载当前页面真正用到的那几块，中文界面通常几百 KB，
 * 而不是全部 4.6MB。
 *
 * 本文件由 scripts/fetch-fonts.sh 生成，不要手工编辑。
 * 字体许可：SIL Open Font License 1.1，允许随产品分发。
 */

'''
open(css_out, 'w', encoding='utf-8').write(header + css)
print(f"▸ 已写入 {css_out}（{css.count('@font-face')} 条 @font-face）")
PY

echo "▸ 完成：$(ls -1 "$OUT_DIR"/*.woff2 | wc -l) 个文件，共 $(du -sh "$OUT_DIR" | cut -f1)"
