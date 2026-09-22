# 乐云企业网盘

按部门授权的私有化企业网盘。目录权限沿组织架构继承，账号只能由超级管理员开通，
文档可在浏览器里直接编辑。

技术栈参考 [dromara/MyObj](https://github.com/dromara/MyObj)（Go + Gin + GORM + Vue3），
核心按企业场景重做：**部门树是权限的骨架**。

```
┌──────────────────────────────────────────────┐
│  一键部署                                     │
│                                              │
│      git clone <仓库地址> && cd Leyun        │
│      ./deploy.sh                             │
│                                              │
│  默认账号 admin / admin —— 登录后请立即修改   │
└──────────────────────────────────────────────┘
```

---

## 目录

- [它解决什么问题](#它解决什么问题)
- [功能](#功能)
- [一键部署](#一键部署)
- [升级与回滚](#升级与回滚)
- [权限模型](#权限模型)
- [Office 在线编辑](#office-在线编辑)
- [知识库 / AI Agent 接入](#知识库--ai-agent-接入)
- [配置](#配置)
- [从源码构建](#从源码构建)
- [接口概览](#接口概览)
- [常见问题](#常见问题)

---

## 它解决什么问题

个人网盘的权限模型是"我的文件 + 分享给某人"。企业不是这么运转的：

- 文件天然属于**部门**，人员流动但资料留在原处；
- 同一份资料，本部门可改、兄弟部门只读、外部门看不见；
- 上级部门的人应该能看下级的资料，反过来不行；
- 账号是**发**给员工的，不是员工自己注册的；
- 谁在什么时候动过哪个文件，必须查得到。

乐云把这些直接做进了数据模型，而不是靠事后加规则补丁。

---

## 功能

**组织与账号**

- 无限层级部门树，支持调整上级（整棵子树跟着搬）
- 三种角色：超级管理员 / 部门管理员 / 普通成员
- **账号只能由超级管理员开通**，系统不提供任何自助注册入口
- 首次初始化写入 `admin / admin`，并持续提醒改密
- 登录失败锁定、账号停用、口令重置、配额分配

**空间与权限**

- 三类空间：个人空间、部门空间、公共空间，各自独立配额
- 7 个权限位：查看 / 下载 / 上传 / 编辑 / 删除 / 分享 / 授权管理
- 4 类授权对象：指定到人 / 指定到部门（可选是否含下级）/ 按角色 / 全员
- 目录级权限沿目录树继承，可在任意一级中断
- **显式拒绝优先于允许**——"整个部门可读，唯独某人除外"能直接表达
- 授权可设有效期，到期自动失效

**文件**

- 拖拽上传（含整个文件夹）、分片上传、断点续传
- 内容寻址去重：相同文件秒传，磁盘上只存一份
- 在线预览：图片、视频、音频、纯文本与代码
- 移动 / 复制 / 重命名 / 跨空间转移 / 目录打包下载
- 回收站：整目录进出，还原回原位置
- 分享链接：内部可见或对外公开，可加提取码、有效期、下载次数上限

**PDF 阅读与编辑**

- 自带 PDF 阅读器（PDF.js），不依赖任何外部服务，打开即用
- 连续滚动、翻页、缩放、适应宽度、旋转、全文搜索并高亮
- 中日韩字符映射表与标准字体随程序一起打包，内网断外网照样正常显示
- **受权限管控**：没有"下载"权限时，阅读器不提供下载、打印，也不生成可复制的文字层
- 需要改内容、加批注、填表单时，一键转到 ONLYOFFICE 的 PDF 编辑器

**Office 在线编辑**

- 对接 ONLYOFFICE Document Server，浏览器内直接编辑 docx / xlsx / pptx / pdf
- 能不能改完全跟着 ACL 走：只有"编辑"权限才进编辑模式，否则只读预览
- 保存自动写回乐云并生成新版本

**知识库 / AI Agent 接入**

- 一组面向程序的只读接口，把云盘当语料源建索引
- 机器身份用独立的 API 密钥，能力按 scope 切分（元数据 / 内容 / 鉴权 / 用户）
- 增量同步：复合游标不重不漏，彻底删除另有墓碑流水可清幽灵数据
- **批量鉴权接口按提问人的真实权限过滤**，跨部门内容不会进模型上下文

**运维**

- 单文件可执行程序（前端已嵌入），也可用 Docker 一键起
- 全量操作审计：谁、何时、对哪个文件、做了什么、成功与否
- SQLite 开箱即用，可切 MySQL / PostgreSQL

---

## 一键部署

### 前置条件

- Linux / macOS，已安装 Docker 与 Docker Compose 插件
- 网盘本体约需 512MB 内存；若要 Office 在线编辑，再留 2GB 给 ONLYOFFICE
- 磁盘预留 4GB（含镜像），实际文件另算

### 部署

```bash
git clone <仓库地址>
cd Leyun
./deploy.sh
```

脚本会依次检查 Docker、检测端口占用、询问参数、生成 `.env` 与 `config.yaml`、
构建镜像、启动容器，最后等到健康检查通过才报成功。首次构建大约 3-8 分钟。

完成后终端会直接打出访问地址与账号：

```
部署完成
────────────────────────────────────────
  访问地址    http://192.168.1.50:8080
  管理员      admin
  初始口令    admin
  文档服务    http://192.168.1.50:8081
────────────────────────────────────────
```

### 常用参数

```bash
./deploy.sh --yes                 # 全用默认值，不提问（适合自动化）
./deploy.sh --no-office           # 不部署 ONLYOFFICE，省约 2GB 内存
./deploy.sh --port 9000           # 换网盘端口
./deploy.sh --office-port 9001    # 换文档服务端口
./deploy.sh --host pan.corp.com   # 指定对外域名
```

脚本可以反复执行。重跑时会**保留已有的 `.env` 密钥**——换掉 JWT 密钥会让所有人当场掉线，
换掉 Office 密钥会让在线编辑握手失败。

### 部署之后做什么

1. 用 `admin / admin` 登录，**立刻到「个人设置」改口令**
2. 到「管理后台 → 部门管理」把公司的组织架构建起来
   （每建一个部门会自动生成同名部门空间，本部门成员默认可读写）
3. 到「管理后台 → 账号管理」逐个开通成员账号，指定部门与角色
4. 按需到各部门空间的「权限设置」里做跨部门授权

---

## 升级与回滚

```bash
./update.sh
```

一条命令走完：拉取最新代码 → **停服备份数据** → 重建镜像 → 重启 → 健康检查。

任何一步失败都会**自动回滚**到升级前的代码与数据，服务不会停在半截状态。

```bash
./update.sh --no-pull      # 不拉代码，只用当前工作区重建（改完配置后常用）
./update.sh --keep 10      # 备份保留份数，默认 5
./update.sh --rollback     # 手动回滚到上一次升级前
./update.sh --no-backup    # 跳过备份（不推荐）
```

备份放在 `backups/<时间戳>/`，包含 `data.tar.gz`、`.env`、`config.yaml`
以及当时的 git 提交号。备份前会先停掉服务——SQLite 正在写的时候直接拷贝，
拿到的会是不一致的快照。

> 回滚会把代码切到备份时记录的提交（游离 HEAD 状态）。之后 `./update.sh`
> 不会再自动拉取新代码，`git checkout <分支名>` 即可恢复跟随分支。

### 数据在哪

全部在 `./data/` 一个目录下：

```
data/
├── leyun.db      # SQLite 数据库（用户、部门、目录树、权限、审计）
├── blobs/        # 去重后的文件内容，按 SHA-256 两级散列存放
└── tmp/          # 分片上传临时目录，可安全清空
```

备份整个系统 = 备份这一个目录。

---

## 权限模型

### 最终权限怎么算出来的

判定"某人在某个目录上能做什么"时，按这个顺序：

```
1. 超级管理员            → 全部权限，任何拒绝都拦不住
2. 个人空间的主人        → 对自己的空间有全部权限
3. 部门管理员            → 对自己管辖子树内的部门空间有全部权限
4. 其余情况走 ACL：
     收集 空间根 + 各级祖先目录 + 目录自身 上挂的规则
     ↓ 过滤：规则是否命中此人、是否过期、是否允许继承
     ↓ 合并：allow 取并集，deny 取并集
     ↓ 最终权限 = allow 去掉 deny        ← 拒绝永远优先
```

### 一条规则由什么组成

| 字段 | 含义 |
|---|---|
| 授权对象 | 某个人 / 某个部门 / 某种角色 / 全体成员 |
| 允许 | 查看、下载、上传、编辑、删除、分享、授权管理 的任意组合 |
| 拒绝 | 同上；**优先级高于任何允许** |
| 含下级部门 | 仅部门授权有效：授权是否下放给子部门的成员 |
| 向下继承 | 关掉后**这条规则**只作用于本目录，不影响子目录 |
| 有效期 | 到期后规则自动失效 |

另外每个目录上还有一个**切断继承**开关：打开后，本目录及其子目录
不再接收上层传下来的任何授权，只认挂在自己身上的规则。

> 两者容易混：「向下继承」是"我这条规则往不往下传"，
> 「切断继承」是"我这个目录收不收上面传来的规则"。

### 几个实际场景

**"研发中心的资料，全研发（含各小组）可读写，市场部只读"**

在研发中心空间根上建两条规则：
- 部门 = 研发中心，含下级部门 ✓，允许 = 协作
- 部门 = 市场部，含下级部门 ✓，允许 = 只读

**"部门资料大家能看，唯独薪酬目录只有总监能进"**

**不要**用拒绝规则去挡部门——拒绝优先于一切允许，总监会被一起挡在外面。

正确做法是切断继承：

1. 薪酬目录 → 权限设置 → **切断继承**
2. 在该目录新增授权：成员 = 总监，允许 = 协作

切断之后，目录内部的授权照常往下传，子目录不必重复配置。

**"给外部审计开三个月的只读权限"**

- 成员 = 审计账号，允许 = 只读，有效期 = 90 天

**"这个目录可以看，但不许下载"**

- 允许 = 仅查看（不勾下载）。这个设定是真的生效的：预览接口只要求"查看"，
  下载与打包接口要求"下载"；PDF 阅读器不出现下载/打印按钮，也不生成可复制的文字层；
  Office 编辑器里的下载、打印、复制同步被禁用。

### 角色能做什么

| | 超级管理员 | 部门管理员 | 普通成员 |
|---|---|---|---|
| 开通 / 删除账号 | ✅ | ❌ | ❌ |
| 建部门、调组织架构 | ✅ | ❌ | ❌ |
| 改系统设置、调配额 | ✅ | ❌ | ❌ |
| 管理本部门及下级空间 | ✅ | ✅ | ❌ |
| 查看本部门成员列表 | ✅ | ✅ | ❌ |
| 查看审计日志 | ✅ | ✅ | ❌ |
| 在有权限的目录里读写 | ✅ | ✅ | ✅ |

**部门管理员不能开通账号**——这是产品的硬约束，API 层与业务层各校验一次。

---

## PDF 阅读与编辑

PDF 有两条路，各管一段：

| | 内置阅读器 | ONLYOFFICE PDF 编辑器 |
|---|---|---|
| 依赖 | 无，前端自带 | 需要 Document Server 8.1+ |
| 打开方式 | 点文件名，默认 | 「更多 → 在线编辑」或阅读器右上角 |
| 能做什么 | 翻页、缩放、旋转、搜索、打印、下载 | 改文字、加批注、填表单，保存写回 |
| 启动速度 | 秒开 | 需等编辑器加载 |

**为什么不直接用 `<iframe>` 套浏览器自带的 PDF 阅读器**：它的工具栏上永远有下载和打印按钮，
用户只要点一下就能把文件存走——「可看不可下」的权限设定形同虚设。
自带阅读器则完全按 ACL 来：

- 有「下载」权限 → 提供下载、打印，生成文字层（可选中、可复制、可搜索）
- 只有「查看」权限 → 工具栏只剩浏览功能，右上角标注「仅查看」，
  页面以画布渲染，**不生成文字层**，选不中也复制不走

> 中日韩字符映射表（cmaps）与标准字体随前端一起打包进二进制，
> 内网完全断外网也能正常显示中文 PDF。

关掉 PDF 在线编辑（例如对接的 Document Server 低于 8.1）：

```bash
# .env
LEYUN_OFFICE_PDF_EDIT=false
```

关掉后内置阅读器照常可用，只是没有「在线编辑」入口。

---

## Office 在线编辑

`./deploy.sh` 默认会一并部署 ONLYOFFICE Document Server 并配好两边的 JWT 密钥，
无需额外操作。文件列表里 docx/xlsx/pptx 会带「可在线编辑」标记，点开即用；
PDF 带「PDF」标记，点开走内置阅读器。

### 能不能编辑由权限决定

- 有"编辑"权限 → 编辑模式，改动自动保存回乐云并生成新版本
- 只有"查看"权限 → 只读预览，工具栏不出现保存
- 没有"下载"权限 → 编辑器里同时禁用下载、打印、复制

可保存回原格式的：`docx` `xlsx` `pptx` `odt` `ods` `odp` `txt` `csv` `pdf`。
`doc` `xls` `ppt` 等旧二进制格式只提供只读预览——转存会丢格式，不如不做。
`djvu` `xps` 同理，ONLYOFFICE 没有对应的写回能力。

### 已有 ONLYOFFICE 想复用

编辑 `.env`：

```bash
LEYUN_OFFICE_ENABLED=true
LEYUN_OFFICE_PUBLIC_URL=http://文档服务器地址:端口    # 浏览器访问的地址
LEYUN_OFFICE_INTERNAL_URL=http://文档服务器地址:端口  # 后端回连的地址
LEYUN_OFFICE_JWT_SECRET=与文档服务器一致的密钥
```

然后 `./update.sh --no-pull`。

若要跳过 ONLYOFFICE 只部署网盘：`./deploy.sh --no-office`。

### 安全设计

- 文档服务器是独立进程，拿不到用户的登录态，靠乐云下发的**短时一次性令牌**取文件
- 保存回调必须带 ONLYOFFICE 签名，验签不过直接拒绝
- 回调里的文件地址**只允许指向配置好的文档服务器主机**，否则后端会变成 SSRF 跳板

---

## 知识库 / AI Agent 接入

云盘里躺着的文档，是做企业知识库最现成的语料。乐云预留了一组 `/api/v1/ai/*`
只读接口，让外部的 RAG 服务或 Agent 能把它抓走建索引——**同时不把部门权限丢掉**。

完整接入步骤、请求响应示例、同步循环伪码和排错表见
**[`docs/ai-integration.md`](docs/ai-integration.md)**。这里只说清楚设计。

### 两把密钥，两件事

后台「API 密钥」页面（仅超级管理员）签发，明文只显示一次。四项能力：

| Scope | 能做什么 |
| --- | --- |
| `index.read` | 枚举空间、文档元数据、删除流水（**不含文件内容**） |
| `content.read` | 读文件原始字节 |
| `acl.check` | 批量判定某人能看哪些节点（读不到任何内容） |
| `user.read` | 按 id / 用户名查用户概要 |

页面上两个预设对应两个角色，**强烈建议分开签发**：

- **索引侧**（`index.read` + `content.read`）：跑在你自己的后台同步进程里。
- **查询侧**（`acl.check` + `user.read`）：跑在在线问答服务里，它读不到文件内容，
  就算泄露也不等于数据泄露。

密钥还能用 `space_ids` 限定空间、设过期时间。服务端只存 SHA-256 指纹。
用户的登录 JWT 进不了 `/ai/*`，API 密钥也进不了普通业务接口——两套身份互不串台。

### 同步：游标 + 墓碑

`GET /ai/documents` 按 `(updated_at, id)` 复合游标翻页，把最后一个 `next_cursor`
存下来，下次就只拿变动过的。用复合游标而不是 `offset`，是因为同步过程中随时有文件
被改动，`offset` 会让记录在翻页之间挪位，造成漏抓或重复。

文件内容建议按 `blob_hash` 取（`GET /ai/blobs/:hash/content`）。乐云是内容寻址存储，
同一份合同被三个部门各存一份时哈希只有一个，按它去重后解析和向量化只做一次。

彻底删除的文件另有 `GET /ai/deletions`：进回收站是软删除、游标扫得到，
但彻底删除是真把行删了，游标永远发现不了，不清理就会在外部索引里留下幽灵数据。

### 过滤：按提问人的权限，而不是靠提示词

```
检索 → 候选 node_id → POST /ai/authorize → 只把 allowed 的片段拼进 prompt → 调模型
```

`/ai/authorize` 一次最多判 1000 个节点，返回 `allowed / denied / missing`
以及每个可见节点的具体权限。判定结果与用户在网页上看到的**完全一致**：同一套 ACL
引擎，继承、显式拒绝、断继承、部门管理员子树全都算在内；停用账号和回收站里的节点
一律判否。仓库里有一条测试专门钉死这两条路径的结果必须逐位相同。

顺序不能反。先把全部命中塞进 prompt、再在系统提示词里叮嘱模型"没权限的别说"，
是拦不住的：内容已经进了上下文，模型的顺从性不是访问控制。

### 四条红线

接入前请通读 [`docs/ai-integration.md`](docs/ai-integration.md) 开头那一节：

1. **索引是数据的第二份拷贝，它不在乐云的权限体系里**——向量库要同等级别地保护，
   且必须存下 `node_id`，否则没法做过滤。
2. **先按提问人权限过滤，再把内容交给模型**——包括引用列表，文件名本身常常就是敏感信息。
3. **云盘内容是不可信输入**——员工上传一份写着"忽略先前的指令"的文档就是一次提示注入，
   带工具的 Agent 尤其要当心。
4. **一把 `content.read` 密钥 = 授权范围内的全量数据**——所以索引侧和查询侧要分开签发。

---

## 配置

配置优先级：**环境变量 > config.yaml > 内置默认值**。完整示例见
[`config.example.yaml`](config.example.yaml)，整份删掉也能启动。

### 常用环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LEYUN_SERVER_PORT` | `8080` | 监听端口 |
| `LEYUN_DB_DRIVER` | `sqlite` | `sqlite` / `mysql` / `postgres` |
| `LEYUN_DB_DSN` | `data/leyun.db` | 数据库连接串 |
| `LEYUN_STORAGE_ROOT` | `data/blobs` | 文件内容目录 |
| `LEYUN_JWT_SECRET` | 自动生成 | 留空则生成后存库，重启不掉线 |
| `LEYUN_ADMIN_USERNAME` | `admin` | 初始超管用户名 |
| `LEYUN_ADMIN_PASSWORD` | `admin` | 初始超管口令 |
| `LEYUN_OFFICE_ENABLED` | `false` | 是否开启 Office 在线编辑 |
| `LEYUN_OFFICE_PUBLIC_URL` | — | 浏览器访问文档服务器的地址 |
| `LEYUN_OFFICE_INTERNAL_URL` | 同 public | 后端回连文档服务器的地址 |
| `LEYUN_OFFICE_JWT_SECRET` | — | 与文档服务器一致的密钥 |
| `LEYUN_OFFICE_PDF_EDIT` | `true` | PDF 在线编辑，需 Document Server 8.1+ |
| `LEYUN_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

### 换成 MySQL

```yaml
database:
  driver: mysql
  dsn: "leyun:口令@tcp(127.0.0.1:3306)/leyun?charset=utf8mb4&parseTime=True&loc=Local"
```

表结构由 GORM 自动迁移，不需要手工执行 SQL。

### 强制改掉初始口令

```yaml
security:
  force_change_default_password: true
```

打开后，仍在用初始口令的账号除了改密接口，调什么都会被拒。

### 放在 Nginx 后面

```nginx
server {
    listen 443 ssl http2;
    server_name pan.corp.com;

    ssl_certificate     /etc/ssl/pan.crt;
    ssl_certificate_key /etc/ssl/pan.key;

    # 大文件分片上传，不要限制请求体
    client_max_body_size 0;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;

        # 上传大文件耗时长，超时给足
        proxy_read_timeout  3600s;
        proxy_send_timeout  3600s;
        proxy_request_buffering off;
    }
}
```

> 走 HTTPS 还有个额外好处：浏览器的 `crypto.subtle` 只在安全上下文可用，
> 裸 HTTP + IP 访问时算不出文件哈希，**秒传会失效**（上传本身不受影响）。

---

## 从源码构建

```bash
make all      # 构建前端 + 后端，产物 bin/leyun
./bin/leyun   # 启动，默认 8080
```

前端产物会落到 `internal/web/dist` 并被 Go 嵌入，`bin/leyun` 是**单个可执行文件**，
拷到目标机器就能跑，不依赖 Node、不依赖前端目录。

### 开发

```bash
make run    # 终端 1：后端 :8080
make dev    # 终端 2：前端 :5173，接口自动代理到 8080
```

### 其他目标

```bash
make test      # Go 测试
make lint      # gofmt + go vet + vue-tsc
make release   # 交叉编译 linux/darwin/windows × amd64/arm64
make docker    # 构建 Docker 镜像
make help      # 看全部目标
```

### 项目结构

```
cmd/leyun/            程序入口
internal/
  ├── config/         配置加载（YAML + 环境变量覆盖）
  ├── model/          数据模型与权限位定义
  ├── store/          数据库连接、自动迁移、首次初始化
  ├── storage/        内容寻址存储、分片合并
  ├── service/        业务逻辑
  │     ├── acl.go        ★ 权限引擎
  │     ├── file.go       目录树、移动复制、回收站
  │     ├── upload.go     分片上传、秒传
  │     ├── office.go     ONLYOFFICE 对接
  │     └── …
  ├── api/            Gin 路由、中间件、HTTP 处理器
  ├── pkg/            JWT、哈希、物化路径、统一返回体、日志
  └── web/            前端资源嵌入
web/                  Vue3 前端
```

---

## 接口概览

全部接口在 `/api/v1` 下，返回体统一为 `{ code, message, data }`，`code` 为 `0` 表示成功。

```
POST   /auth/login                登录
POST   /auth/register             固定返回 403 —— 本系统不开放自助注册
GET    /auth/profile              当前用户 + 可见空间 + 权限目录
POST   /auth/password             本人改密

GET    /spaces                    我能看到的空间
GET    /files?space_id&parent_id  列目录（附带每个条目的实际权限）
POST   /files/folder              新建目录
POST   /files/move | /copy | /trash | /rename
GET    /files/:id/download        下载（支持 Range 断点续传；目录自动打包 zip）
GET    /files/:id/preview         在线预览（只要求"查看"权限）

POST   /upload                    小文件直传
POST   /upload/init               初始化分片上传（命中秒传则直接返回文件）
POST   /upload/chunk              上传分片
POST   /upload/complete           合并分片

GET    /acl?space_id&node_id      查看权限（含继承来源）
POST   /acl                       授权 / 改权限
DELETE /acl/:id                   撤销授权

POST   /shares                    创建分享
GET    /share/:code/info          访问分享（匿名可访问，需提取码时返回 42901）

GET    /office/config             下发编辑器配置
GET    /office/content            文档服务器取文件（凭一次性令牌）
POST   /office/callback           文档服务器保存回调

GET    /admin/overview            管理后台概览
POST   /admin/users               开通账号（仅超级管理员）
POST   /admin/departments         新建部门（仅超级管理员）
GET    /admin/audit-logs          审计日志
POST   /admin/api-keys            签发 AI 接入密钥（仅超级管理员，明文只返回一次）
```

面向程序的只读接口走 API 密钥，不认登录态（详见
[`docs/ai-integration.md`](docs/ai-integration.md)）：

```
GET    /ai/whoami                 自检这把密钥有什么能力
GET    /ai/spaces                 可抓取的空间概要            index.read
GET    /ai/documents              枚举文档（复合游标增量）     index.read
GET    /ai/deletions              彻底删除的流水              index.read
GET    /ai/documents/:id/content  按节点取原始内容            content.read
GET    /ai/blobs/:hash/content    按内容哈希取原始内容（推荐） content.read
POST   /ai/authorize              批量判定某人能看哪些节点     acl.check
GET    /ai/users/:id              用户概要                   user.read
```

---

## 常见问题

**忘了超管口令怎么办？**

如果还有另一个超管账号能登录，直接在「账号管理」里重置即可。

都进不去的话，在服务器上执行应急重置（需要能访问数据库文件，因此没有远程风险）：

```bash
docker exec leyun /app/leyun -config /app/config.yaml -reset-password admin
```

会打印一个随机新口令，账号同时被标记为"必须改密"，登录后立刻改掉即可。
源码部署直接 `./bin/leyun -reset-password admin`。

> 初始账号只在库中**没有任何超级管理员**时才会写入，
> 所以改 `LEYUN_ADMIN_USERNAME` 重启是没有用的。

**为什么秒传有时候不生效？**

浏览器算文件哈希依赖 `crypto.subtle`，它只在 HTTPS 或 `localhost` 下可用。
用 `http://内网IP` 访问时算不出哈希，会退化为正常上传。配上 HTTPS 即可恢复。

**删了部门，里面的文件去哪了？**

不会去哪——有成员、有下级部门或空间内还有文件时，系统会直接拒绝删除，
要求先把内容转移走。这是刻意的：企业数据不该因为一次误操作静默消失。

**回收站会自动清空吗？**

回收站条目会一直保留到手动清空。`system.trash_retention_days` 是预留的策略配置项，
当前版本不做自动物理删除。

**能对接企业微信 / 钉钉 / LDAP 吗？**

当前版本没做。认证入口集中在 `internal/service/auth.go`，
接入外部身份源时在那里加一条分支即可，账号仍然由超管在后台创建。

**支持 WebDAV 吗？**

当前版本没做。

---

## 许可证

[Apache License 2.0](LICENSE)
