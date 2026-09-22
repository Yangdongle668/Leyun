# 权限模型详解

这份文档面向两类人：要配置权限的管理员，以及要改权限代码的开发者。

实现集中在 `internal/service/acl.go`，权限位定义在 `internal/model/permission.go`，
测试在 `internal/service/acl_test.go`。

---

## 1. 为什么不用"文件夹共享"那一套

个人网盘的模型是「我的文件 + 分享给某人」。企业里这套不成立：

- 文件属于**部门**，人走了资料要留下；
- 权限要沿组织架构继承：上级部门看得到下级的，反过来不行；
- 要能表达"整个部门可读，唯独某人除外"；
- 要能表达"可以看，但不许下载"。

所以乐云的权限有两层：**空间**决定文件归谁管，**ACL** 决定谁能对它做什么。

---

## 2. 权限位

7 个独立的位，可以任意组合（`internal/model/permission.go`）：

| 位 | 码 | 含义 |
|---|---|---|
| `PermView` | `view` | 浏览目录、查看文件信息、在线预览 |
| `PermDownload` | `download` | 下载原文件、打包下载目录 |
| `PermUpload` | `upload` | 上传文件、新建子目录 |
| `PermEdit` | `edit` | 重命名、移动、Office 在线编辑保存 |
| `PermDelete` | `delete` | 删除到回收站、彻底删除、还原 |
| `PermShare` | `share` | 创建分享链接 |
| `PermManage` | `manage` | 管理该位置的权限 |

**`view` 与 `download` 是分开的**，这样"可看不可下"才是真的成立：
预览接口只校验 `view`，下载与打包接口校验 `download`。

### 预设组合

| 组合 | 包含 | 用在哪 |
|---|---|---|
| `PermReadOnly` | view + download | 只读授权 |
| `PermWrite` | 只读 + upload + edit + delete | 资料只在部门内流转的目录 |
| `PermCollaborate` | 读写 + share | **部门空间的默认授权** |
| `PermAll` | 全部 7 位 | 完全控制 |

部门空间默认给 `PermCollaborate` 而不是 `PermWrite`：同事之间连发个内部链接都做不到的
共享盘没人会用。对外公开分享另有系统级开关把关。

### 隐含依赖

`Permission.Normalize()` 补齐两条规则：

- 任何操作都以"能看见"为前提 → 只给 `download` 会自动补上 `view`；
- 能改权限的人必然拥有全部业务权限 → `manage` 展开为 `PermAll`，
  否则会出现"能改权限却打不开目录"的死角。

---

## 3. 三类空间

| 类型 | 归属 | 默认可见性 |
|---|---|---|
| 个人空间 | 某个用户 | 只有本人（与超管）可见 |
| 部门空间 | 某个部门 | 本部门及下级成员默认可读写可分享 |
| 公共空间 | 全公司 | 全员默认只读 |

新建部门会自动创建同名部门空间，并写入一条默认授权；
新建账号会自动创建个人空间。

---

## 4. 一条 ACL 规则

```go
type AccessRule struct {
    SpaceID        uint64         // 作用的空间
    NodeID         uint64         // 0 = 整个空间；否则是某个目录/文件
    PrincipalType  PrincipalType  // user / dept / role / everyone
    PrincipalID    uint64         // 用户 ID 或部门 ID
    PrincipalRole  Role           // PrincipalType=role 时有效
    Allow          Permission     // 允许的权限位
    Deny           Permission     // 拒绝的权限位（优先级更高）
    IncludeSubDept bool           // 部门授权是否下放给子部门成员
    Inheritable    bool           // 这条规则是否向子目录继承
    ExpireAt       *time.Time     // 到期自动失效
}
```

同一个 `(空间, 节点, 授权对象)` 只保留一条记录，重复授权是更新而不是追加
（`ACLService.SaveRule`）。

### 还有一个开关挂在目录上

```go
type Node struct {
    // ...
    ACLIsolated bool  // true = 本目录切断继承，不收上层传下来的任何授权
}
```

两者容易混，一句话区分：

- `Rule.Inheritable` —— **我这条规则往不往下传**；
- `Node.ACLIsolated` —— **我这个目录收不收上面传来的规则**。

切断继承是"这个目录只给某几个人"的唯一正确做法，
用拒绝规则去挡部门会把想放行的那个人一起挡住。

---

## 5. 最终权限怎么算

`ACLService.Effective(subject, space, node)`：

```
1. 超级管理员                     → PermAll，任何 deny 都拦不住
2. 空间已停用                     → PermNone（超管除外）
3. 个人空间且是本人               → PermAll
4. 部门空间且是该子树的部门管理员  → PermAll
5. 其余走 ACL：
     先定收集范围（scopeNodeIDs）：
       从节点自身往上走，逐级加入；
       遇到第一个 ACLIsolated 的目录就停在那一级，
       没遇到则一直收到空间根（node_id = 0）。
     取出这些 node_id 上的全部规则，逐条过滤：
       ├ 过期了？                        → 丢弃
       ├ 挂在自身节点上？                 → 保留（不看 Inheritable）
       ├ 来自祖先或空间根，且 Inheritable  → 保留
       └ 否则                            → 丢弃
     再按授权对象匹配当前用户，命中的：
       allow |= rule.Allow
       deny  |= rule.Deny
     最终 = (allow &^ deny).Normalize() &^ deny
```

最后再减一次 `deny` 不是冗余：`Normalize()` 可能补回 `view`
（例如只给了 `download`），如果 `view` 正是被显式拒绝的那一位，必须再减掉。

### 授权对象怎么匹配到人

```go
switch rule.PrincipalType {
case everyone: 命中所有登录用户
case role:     rule.PrincipalRole == 用户的角色
case user:     rule.PrincipalID == 用户 ID
case dept:
    // 直属部门永远命中
    if rule.PrincipalID == 用户的直属部门 { 命中 }
    // 上级部门只有勾了"含下级"才下放
    if rule.IncludeSubDept && rule.PrincipalID ∈ 用户部门的祖先链 { 命中 }
}
```

祖先链来自部门表的**物化路径**：部门 `Path` 形如 `/1/4/9/`，
一次 `LIKE '/1/4/%'` 就能捞出整棵子树，不需要递归查询。

---

## 6. 列目录时的批量计算

逐个节点调 `Effective` 会产生 N 次查询。`EffectiveForChildren` 的做法是：
先算出父目录的权限作为基线，再一次性查出挂在这批子节点**自身**上的规则做增量合并。

`acl_test.go` 里有一条用例专门比对两种算法的结果必须一致——
不一致就会出现"列表里看得见、点进去打不开"。

---

## 7. 常见场景怎么配

### 研发资料：全研发可读写，市场部只读

在研发中心空间根（`node_id = 0`）上建两条：

| 对象 | 含下级 | 允许 |
|---|---|---|
| 部门 = 研发中心 | ✓ | 协作 |
| 部门 = 市场部 | ✓ | 只读 |

### 部门资料大家能看，薪酬目录只有总监能进

**不要**在薪酬目录上加"拒绝"——拒绝优先于一切允许，总监会被一起挡在外面。

正确做法是**切断继承**：在薪酬目录的权限设置里点「切断继承」，
本目录及其子目录就不再接收上层传下来的任何授权；再单独授权给总监即可。

| 步骤 | 操作 |
|---|---|
| 1 | 薪酬目录 → 权限设置 → 切断继承 |
| 2 | 新增授权：成员 = 总监，允许 = 协作 |

切断之后，本目录**内部**的授权照常向下继承，子目录不必重复配置。

> 一句话记法：
> **"只有某些人能进"用切断继承；"除了某人都能进"用显式拒绝。**

### 整个部门可读，唯独某人除外

| 对象 | 允许 | 拒绝 |
|---|---|---|
| 部门 = 市场部（含下级） | 只读 | — |
| 成员 = 李四 | — | 查看、下载 |

### 给外部审计开三个月只读

| 对象 | 允许 | 有效期 |
|---|---|---|
| 成员 = 审计账号 | 只读 | 90 天 |

### 可以看，但不许下载

授权时只勾"查看"，不勾"下载"。这条在三处同时兑现，不是只拦一个接口了事：

| 环节 | 有 download | 只有 view |
|---|---|---|
| 接口 | 下载与打包接口放行 | 只有预览接口放行 |
| PDF 阅读器 | 下载、打印按钮可用，生成文字层（可选中复制、可搜索） | 按钮消失，标注"仅查看"，只以画布渲染、**不生成文字层** |
| Office 编辑器 | `download` / `print` / `copy` 均为 true | 三者同时为 false |

> 早期版本的 PDF 预览是 `<iframe>` 套浏览器自带阅读器——它的工具栏上永远带着
> 下载和打印按钮，这条权限等于白设。换成自带阅读器就是为了堵上这个口子。

---

## 8. 角色边界

| | 超级管理员 | 部门管理员 | 普通成员 |
|---|---|---|---|
| 开通 / 删除 / 停用账号 | ✅ | ❌ | ❌ |
| 重置他人口令 | ✅ | ❌ | ❌ |
| 建部门、调组织架构 | ✅ | ❌ | ❌ |
| 改系统设置、调配额 | ✅ | ❌ | ❌ |
| 管理本部门及下级的空间 | ✅ | ✅ | ❌ |
| 查看本部门成员列表 | ✅ | ✅（限本子树） | ❌ |
| 查看审计日志 | ✅ | ✅ | ❌ |

**账号只能由超级管理员开通**，这条在两处强制：

- 路由层：`/admin/users` 的写操作挂了 `RequireSuperAdmin()` 中间件；
- 业务层：`UserService.Create` 头一行就校验 `operator.IsSuperAdmin()`。

两道都在，是为了将来新增入口时不会绕过这条约束。
`/auth/register` 这类路径存在，但固定返回 403 并说明账号由管理员开通——
比让它落到前端路由上显示一个空白页要清楚。

---

## 9. 授权时的提权防护

`handler.Grant` 里有一条检查：

```go
if !subj.IsSuperAdmin() && !granterPerm.Has(allow) {
    return 403 // 不能授予超出自身权限的能力
}
```

没有这条，一个只有 `manage` 而业务权限受限的人就能给自己授出完全控制。

分享同理（`ShareService.Create`）：分享出去的权限会先与分享者自己的权限取交集，
并且只允许 `view` / `download` / `upload` 三项。

---

## 10. 改代码时要注意的两件事

### 事务里不要走连接池

SQLite 的连接池设成了 1（`internal/store/store.go`）。
在 `db.Transaction(func(tx *gorm.DB){...})` 里再用 `s.db` 发查询，
会等一个永远不会空出来的连接，**整个进程会死锁**。

所有涉及批量写的操作（移动、复制、删除、还原、清空回收站）
都遵循**"先校验、后在事务内只写"**：权限判定和节点加载全部前置，
事务里只用传入的 `tx`。

### 布尔字段不要写 `default` 标签

GORM 在 INSERT 时会**跳过带 `default` 标签字段的零值**，让数据库默认值生效。
对 `Inheritable` / `IncludeSubDept` 这种权限边界字段是致命的：
管理员取消勾选存进去的 `false` 会被悄悄改回 `true`。

这两个字段以及 `Enabled` 都刻意不写 `default`，由创建方显式赋值。
`acl_test.go` 里的 `TestNonInheritableRuleStaysOnItsNode` 就是为了守住这条。
