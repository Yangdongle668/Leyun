// 构建完把 internal/web/dist/.gitkeep 放回去。
//
// vite 配了 emptyOutDir: true，每次构建都会把 outDir 清空，
// 连这个提交进仓库的占位文件一起删掉。后果有两个：
//
//   1. 每跑一次前端构建，git 就多一条 "deleted: .gitkeep"，
//      工作区莫名其妙变脏；
//   2. 真有人顺手把这条删除提交上去，干净 clone 就又编译不了了——
//      internal/web/embed.go 是 //go:embed all:dist，
//      Go 要求这个目录至少有一个文件。
//
// 由 package.json 的 postbuild 自动触发，不需要手工执行。

import { writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const target = resolve(here, '../../internal/web/dist/.gitkeep')

// 内容必须和仓库里那份逐字一致，否则构建完照样是一条 modified。
const content = `这个文件是有用的，别删。

internal/web/embed.go 里写的是 //go:embed all:dist，Go 要求这个目录
至少有一个文件，否则编译直接报 "pattern all:dist: no matching files found"。
刚 clone 下来还没构建前端时，全靠它撑着，不然 go build / go test 都跑不起来。

真正的前端产物由 \`make web\` 或 Docker 的构建阶段生成，都被 .gitignore
挡在仓库外；只有这一个文件例外（见 .gitignore 的 ! 那行）。

没构建前端就启动的话，服务会返回 embed.go 里那个兜底页面，
上面写着该跑什么命令。
`

await writeFile(target, content, 'utf8')
console.log('▸ 已放回 internal/web/dist/.gitkeep')
