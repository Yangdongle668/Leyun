// 把 pdf.js 的字符映射表与标准字体拷进 public/，随前端一起打包。
//
// 为什么必须带上：
//   - cmaps 决定中日韩文字能不能正确显示，缺了就是一屏乱码或空白；
//   - standard_fonts 是 PDF 里没有内嵌字体时的兜底字形。
// 内网部署拿不到 CDN，这两份资源只能自己带。
//
// 由 package.json 的 prebuild / predev 自动触发，不需要手工执行。

import { cp, mkdir, rm, stat } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const webRoot = resolve(here, '..')
const pdfjs = resolve(webRoot, 'node_modules/pdfjs-dist')
const target = resolve(webRoot, 'public/pdfjs')

const assets = ['cmaps', 'standard_fonts']

async function exists(p) {
  try {
    await stat(p)
    return true
  } catch {
    return false
  }
}

if (!(await exists(pdfjs))) {
  console.error('[pdfjs] 找不到 pdfjs-dist，请先执行 npm install')
  process.exit(1)
}

// 每次全量重来，避免升级 pdf.js 后留下上一版的残留文件。
await rm(target, { recursive: true, force: true })
await mkdir(target, { recursive: true })

for (const name of assets) {
  const from = resolve(pdfjs, name)
  if (!(await exists(from))) {
    console.warn(`[pdfjs] 跳过缺失的资源目录：${name}`)
    continue
  }
  await cp(from, resolve(target, name), { recursive: true })
  console.log(`[pdfjs] 已复制 ${name}`)
}
