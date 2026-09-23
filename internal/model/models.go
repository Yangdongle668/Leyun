// Package model 定义乐云企业网盘的持久化实体。
package model

import (
	"time"
)

// Role 是全局角色。乐云只区分三种全局身份，具体到目录的能力由 ACL 决定。
type Role string

const (
	// RoleSuperAdmin 超级管理员：系统内唯一可以开通账号的角色，拥有全部数据的最终控制权。
	RoleSuperAdmin Role = "super_admin"
	// RoleDeptAdmin 部门管理员：管理本部门（含子部门）空间的目录与授权，但不能开通账号。
	RoleDeptAdmin Role = "dept_admin"
	// RoleMember 普通成员。
	RoleMember Role = "member"
)

// Valid 判断角色是否合法。
func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleDeptAdmin, RoleMember:
		return true
	}
	return false
}

// Label 返回角色中文名。
func (r Role) Label() string {
	switch r {
	case RoleSuperAdmin:
		return "超级管理员"
	case RoleDeptAdmin:
		return "部门管理员"
	case RoleMember:
		return "普通成员"
	}
	return string(r)
}

// UserStatus 是账号状态。
type UserStatus string

const (
	// UserActive 正常。
	UserActive UserStatus = "active"
	// UserDisabled 已停用：保留数据但禁止登录。
	UserDisabled UserStatus = "disabled"
)

// SpaceType 区分三类空间。
type SpaceType string

const (
	// SpacePersonal 个人空间，仅归属用户本人（及超管）可见。
	SpacePersonal SpaceType = "personal"
	// SpaceDepartment 部门空间，按部门授权。
	SpaceDepartment SpaceType = "department"
	// SpacePublic 公共空间，全员可见（默认只读）。
	SpacePublic SpaceType = "public"
)

// PrincipalType 是授权对象的类型。
type PrincipalType string

const (
	// PrincipalUser 指定到人。
	PrincipalUser PrincipalType = "user"
	// PrincipalDept 指定到部门。
	PrincipalDept PrincipalType = "dept"
	// PrincipalRole 指定到全局角色。
	PrincipalRole PrincipalType = "role"
	// PrincipalEveryone 全体登录用户。
	PrincipalEveryone PrincipalType = "everyone"
)

// Valid 判断授权对象类型是否合法。
func (p PrincipalType) Valid() bool {
	switch p {
	case PrincipalUser, PrincipalDept, PrincipalRole, PrincipalEveryone:
		return true
	}
	return false
}

// Department 是部门树节点。
//
// Path 是物化路径，形如 "/1/4/9/"，既包含自身也包含全部祖先，
// 借助 `LIKE '/1/4/%'` 即可一次性捞出子树，避免递归查询。
type Department struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	ParentID uint64 `gorm:"index;not null;default:0" json:"parent_id"`
	Name     string `gorm:"size:128;not null" json:"name"`
	Code     string `gorm:"size:64;index" json:"code"`
	Path     string `gorm:"size:512;index;not null" json:"path"`
	Depth    int    `gorm:"not null;default:0" json:"depth"`
	Sort     int    `gorm:"not null;default:0" json:"sort"`
	LeaderID uint64 `gorm:"index;not null;default:0" json:"leader_id"`
	Remark   string `gorm:"size:255" json:"remark"`
	// Enabled 等布尔字段刻意不写 default 标签：GORM 在 INSERT 时会跳过带默认值字段的零值，
	// 于是显式传入的 false 会被数据库默认值悄悄改成 true。创建方一律显式赋值。
	Enabled   bool      `gorm:"not null" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Department) TableName() string { return "ly_department" }

// User 是账号。乐云没有注册入口，所有账号都由超级管理员开通，CreatedBy 记录开通人。
type User struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	Nickname     string     `gorm:"size:64" json:"nickname"`
	Email        string     `gorm:"size:128;index" json:"email"`
	Phone        string     `gorm:"size:32;index" json:"phone"`
	JobTitle     string     `gorm:"size:64" json:"job_title"`
	DeptID       uint64     `gorm:"index;not null;default:0" json:"dept_id"`
	Role         Role       `gorm:"size:32;index;not null;default:member" json:"role"`
	Status       UserStatus `gorm:"size:16;index;not null;default:active" json:"status"`
	// QuotaBytes 个人空间配额，0 表示不限制。
	QuotaBytes int64 `gorm:"not null;default:0" json:"quota_bytes"`
	// MustChangePassword 为 true 时，除改密外的接口一律拒绝（首次使用默认口令的账号会被打上该标记）。
	MustChangePassword bool       `gorm:"not null;default:false" json:"must_change_password"`
	LoginFailures      int        `gorm:"not null;default:0" json:"-"`
	LockedUntil        *time.Time `json:"locked_until,omitempty"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	LastLoginIP        string     `gorm:"size:64" json:"last_login_ip,omitempty"`
	CreatedBy          uint64     `gorm:"index;not null;default:0" json:"created_by"`
	Remark             string     `gorm:"size:255" json:"remark"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (User) TableName() string { return "ly_user" }

// IsSuperAdmin 判断是否超级管理员。
func (u *User) IsSuperAdmin() bool { return u != nil && u.Role == RoleSuperAdmin }

// Space 是一个独立的文件空间（个人 / 部门 / 公共）。
type Space struct {
	ID   uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Type SpaceType `gorm:"size:16;index;not null" json:"type"`
	Name string    `gorm:"size:128;not null" json:"name"`
	// OwnerID 个人空间的归属用户；其它类型为 0。
	OwnerID uint64 `gorm:"index;not null;default:0" json:"owner_id"`
	// DeptID 部门空间对应的部门；其它类型为 0。
	DeptID uint64 `gorm:"index;not null;default:0" json:"dept_id"`
	// QuotaBytes 空间配额，0 表示不限制。
	QuotaBytes int64 `gorm:"not null;default:0" json:"quota_bytes"`
	UsedBytes  int64 `gorm:"not null;default:0" json:"used_bytes"`
	// 同 Department.Enabled：不设 default，避免显式的 false 被吞掉。
	Enabled   bool      `gorm:"not null" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Space) TableName() string { return "ly_space" }

// Node 是空间内的一个目录或文件。目录树用 ParentID + Path 双写，便于移动子树时批量改路径。
type Node struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	SpaceID  uint64 `gorm:"index:idx_node_space_parent;not null" json:"space_id"`
	ParentID uint64 `gorm:"index:idx_node_space_parent;not null;default:0" json:"parent_id"`
	Name     string `gorm:"size:255;not null" json:"name"`
	IsDir    bool   `gorm:"index;not null;default:false" json:"is_dir"`
	// Path 是含自身 ID 的物化路径，形如 "/3/18/42/"，根目录为 "/"。
	Path  string `gorm:"size:1024;index;not null" json:"path"`
	Depth int    `gorm:"not null;default:0" json:"depth"`
	Size  int64  `gorm:"not null;default:0" json:"size"`
	// BlobHash 指向去重后的实际内容；目录为空。
	BlobHash string `gorm:"size:64;index" json:"blob_hash,omitempty"`
	MimeType string `gorm:"size:128" json:"mime_type,omitempty"`
	Ext      string `gorm:"size:32;index" json:"ext,omitempty"`
	Version  int    `gorm:"not null;default:1" json:"version"`
	// ACLIsolated 为 true 时，本目录不再接收上层传下来的授权，只认挂在自己身上的规则。
	//
	// 这是"这个目录只给某几个人"的唯一正确做法：用拒绝规则去挡部门是不行的，
	// 拒绝优先于一切允许，会把你想放行的那个人一起挡在外面。
	// 同 Enabled，不写 default 标签，避免显式的 false 被 GORM 吞掉。
	ACLIsolated bool `gorm:"not null" json:"acl_isolated"`
	// Trashed 标记回收站条目。TrashRootID 指向本次删除操作的顶层节点，
	// 用于"整目录还原"——子节点跟随顶层节点一起进出回收站。
	Trashed     bool       `gorm:"index;not null;default:false" json:"trashed"`
	TrashRootID uint64     `gorm:"index;not null;default:0" json:"trash_root_id,omitempty"`
	TrashedAt   *time.Time `json:"trashed_at,omitempty"`
	TrashedBy   uint64     `gorm:"not null;default:0" json:"trashed_by,omitempty"`
	// TrashParentID 保存删除前的父目录，用于还原。
	TrashParentID uint64    `gorm:"not null;default:0" json:"-"`
	CreatedBy     uint64    `gorm:"index;not null;default:0" json:"created_by"`
	UpdatedBy     uint64    `gorm:"not null;default:0" json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	// UpdatedAt 必须建索引：外部索引程序靠它做增量游标，
	// 没有索引的话每次同步都是一次全表扫。
	UpdatedAt time.Time `gorm:"index:idx_node_updated" json:"updated_at"`
}

// TableName 指定表名。
func (Node) TableName() string { return "ly_node" }

// Blob 是去重后的物理文件。同一份内容在磁盘上只存一份，由 RefCount 控制回收。
type Blob struct {
	Hash      string    `gorm:"primaryKey;size:64" json:"hash"`
	Size      int64     `gorm:"not null" json:"size"`
	RefCount  int64     `gorm:"not null;default:0" json:"ref_count"`
	StorePath string    `gorm:"size:512;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Blob) TableName() string { return "ly_blob" }

// AccessRule 是一条 ACL 授权/拒绝记录。
//
// NodeID 为 0 表示作用于整个空间；否则作用于该目录（或文件）。
// Allow 与 Deny 都是位掩码，Deny 优先级更高——企业里"某人除外"是刚需。
type AccessRule struct {
	ID            uint64        `gorm:"primaryKey;autoIncrement" json:"id"`
	SpaceID       uint64        `gorm:"index:idx_rule_space_node;not null" json:"space_id"`
	NodeID        uint64        `gorm:"index:idx_rule_space_node;not null;default:0" json:"node_id"`
	PrincipalType PrincipalType `gorm:"size:16;index;not null" json:"principal_type"`
	// PrincipalID 对应用户 ID / 部门 ID；PrincipalRole 时存角色名的哈希无意义，改用 PrincipalRoleName。
	PrincipalID uint64 `gorm:"index;not null;default:0" json:"principal_id"`
	// PrincipalRole 仅当 PrincipalType=role 时有效。
	PrincipalRole Role `gorm:"size:32" json:"principal_role,omitempty"`
	// Allow / Deny 权限位。
	Allow Permission `gorm:"not null;default:0" json:"allow"`
	Deny  Permission `gorm:"not null;default:0" json:"deny"`
	// IncludeSubDept 仅当 PrincipalType=dept 时有效：是否把授权下放给子部门成员。
	//
	// 这两个开关都不写 default 标签：它们是权限边界，
	// 管理员取消勾选后必须真的存成 false，绝不能被数据库默认值改回 true。
	IncludeSubDept bool `gorm:"not null" json:"include_sub_dept"`
	// Inheritable 为 false 时，该规则只作用于本目录，不向子目录继承。
	Inheritable bool       `gorm:"not null" json:"inheritable"`
	ExpireAt    *time.Time `json:"expire_at,omitempty"`
	Remark      string     `gorm:"size:255" json:"remark,omitempty"`
	CreatedBy   uint64     `gorm:"not null;default:0" json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (AccessRule) TableName() string { return "ly_access_rule" }

// ShareScope 区分分享的可见范围。
type ShareScope string

const (
	// ShareInternal 仅企业内部登录用户可访问（可再限定到部门/人）。
	ShareInternal ShareScope = "internal"
	// SharePublic 任何拿到链接的人都能访问（可加提取码）。
	SharePublic ShareScope = "public"
)

// Share 是一条分享记录。
type Share struct {
	ID      uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Code    string     `gorm:"size:32;uniqueIndex;not null" json:"code"`
	SpaceID uint64     `gorm:"index;not null" json:"space_id"`
	NodeID  uint64     `gorm:"index;not null" json:"node_id"`
	Scope   ShareScope `gorm:"size:16;not null;default:internal" json:"scope"`
	// PasswordHash 为空表示无需提取码。
	PasswordHash string `gorm:"size:255" json:"-"`
	HasPassword  bool   `gorm:"-" json:"has_password"`
	// Perms 访客在该分享下的权限，只允许 view/download/upload 的子集。
	Perms        Permission `gorm:"not null;default:0" json:"perms"`
	ExpireAt     *time.Time `json:"expire_at,omitempty"`
	MaxDownloads int64      `gorm:"not null;default:0" json:"max_downloads"`
	Downloads    int64      `gorm:"not null;default:0" json:"downloads"`
	Views        int64      `gorm:"not null;default:0" json:"views"`
	Revoked      bool       `gorm:"not null;default:false" json:"revoked"`
	CreatedBy    uint64     `gorm:"index;not null" json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (Share) TableName() string { return "ly_share" }

// ShareTarget 限定内部分享的可见对象；一条分享没有任何 target 时表示全员可见。
type ShareTarget struct {
	ID            uint64        `gorm:"primaryKey;autoIncrement" json:"id"`
	ShareID       uint64        `gorm:"index;not null" json:"share_id"`
	PrincipalType PrincipalType `gorm:"size:16;not null" json:"principal_type"`
	PrincipalID   uint64        `gorm:"not null;default:0" json:"principal_id"`
}

// TableName 指定表名。
func (ShareTarget) TableName() string { return "ly_share_target" }

// UploadSession 记录一次分片上传的进度，支持断点续传。
type UploadSession struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UploadID string `gorm:"size:64;uniqueIndex;not null" json:"upload_id"`
	UserID   uint64 `gorm:"index;not null" json:"user_id"`
	SpaceID  uint64 `gorm:"not null" json:"space_id"`
	ParentID uint64 `gorm:"not null;default:0" json:"parent_id"`
	Filename string `gorm:"size:255;not null" json:"filename"`
	Size     int64  `gorm:"not null" json:"size"`
	// ChunkSize 与 ChunkCount 在 init 时确定，之后不可更改。
	ChunkSize  int64 `gorm:"not null" json:"chunk_size"`
	ChunkCount int   `gorm:"not null" json:"chunk_count"`
	// Hash 是整文件的 SHA-256，用于秒传与完整性校验；可为空（未知时在合并阶段计算）。
	Hash string `gorm:"size:64;index" json:"hash,omitempty"`
	// ReceivedMask 是分片到达位图（JSON 数组，元素为已收到的分片序号）。
	ReceivedMask string `gorm:"type:text" json:"-"`
	// Conflict 是重名时的处理方式，见 service.ConflictMode。空串按"保留两者"处理。
	Conflict  string `gorm:"size:16" json:"conflict,omitempty"`
	Completed bool   `gorm:"not null;default:false" json:"completed"`
	// NodeID 是合并完成后生成（或被覆盖）的文件节点。
	//
	// 记下来是为了让 complete 接口可以重试：合并成功但响应在路上丢了的时候，
	// 前端重发一次不该变成"该上传已完成"的报错，而应该拿回同一个节点。
	NodeID     uint64     `gorm:"not null;default:0" json:"node_id,omitempty"`
	ExpireAt   time.Time  `gorm:"index;not null" json:"expire_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// TableName 指定表名。
func (UploadSession) TableName() string { return "ly_upload_session" }

// AuditLog 是操作审计。企业网盘的合规底线：谁、什么时候、对哪个文件做了什么。
type AuditLog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint64    `gorm:"index;not null;default:0" json:"user_id"`
	Username   string    `gorm:"size:64;index" json:"username"`
	DeptID     uint64    `gorm:"index;not null;default:0" json:"dept_id"`
	Action     string    `gorm:"size:64;index;not null" json:"action"`
	TargetType string    `gorm:"size:32" json:"target_type"`
	TargetID   uint64    `gorm:"index;not null;default:0" json:"target_id"`
	Target     string    `gorm:"size:512" json:"target"`
	Detail     string    `gorm:"type:text" json:"detail"`
	Success    bool      `gorm:"index;not null;default:true" json:"success"`
	IP         string    `gorm:"size:64" json:"ip"`
	UserAgent  string    `gorm:"size:255" json:"user_agent"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// TableName 指定表名。
func (AuditLog) TableName() string { return "ly_audit_log" }

// APIKey 是给机器用的身份凭证，供外部 AI Agent、同步程序等调用开放接口。
//
// 与用户令牌的区别：用户令牌代表"某个人"，权限完全跟着 ACL 走；
// API Key 代表"某个程序"，能力由 Scopes 显式列举，默认什么都做不了。
type APIKey struct {
	ID   uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Name string `gorm:"size:64;not null" json:"name"`
	// Prefix 是密钥的前若干位明文，只用于在界面上认出是哪一把，不足以还原密钥。
	Prefix string `gorm:"size:16;index;not null" json:"prefix"`
	// KeyHash 是密钥的 SHA-256。
	//
	// 这里刻意不用 bcrypt：API Key 是高熵随机串，不存在被字典爆破的问题，
	// 而每个请求都要验一次，bcrypt 那几十毫秒会直接压垮同步任务。
	KeyHash string `gorm:"size:64;uniqueIndex;not null" json:"-"`
	// Scopes 是逗号分隔的能力清单，见 service 层的 Scope* 常量。
	Scopes string `gorm:"size:255;not null" json:"scopes"`
	// SpaceIDs 是逗号分隔的空间白名单，为空表示不限空间。
	// 想做"只读公共空间的知识库"，在这里限定即可。
	SpaceIDs   string     `gorm:"size:512" json:"space_ids"`
	Enabled    bool       `gorm:"not null" json:"enabled"`
	ExpireAt   *time.Time `json:"expire_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `gorm:"size:64" json:"last_used_ip,omitempty"`
	CreatedBy  uint64     `gorm:"index;not null;default:0" json:"created_by"`
	Remark     string     `gorm:"size:255" json:"remark"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (APIKey) TableName() string { return "ly_api_key" }

// NodeTombstone 记录被彻底删除的节点。
//
// 增量同步靠 updated_at 游标就能覆盖新建、改名、移动、进出回收站——这些都会刷新
// updated_at。唯独"彻底删除"是真的把行删掉，游标永远扫不到，
// 外部索引里就会留下一条指向已不存在文件的幽灵数据。所以单独记一笔墓碑。
type NodeTombstone struct {
	// ID 自增，同时充当增量游标。
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"seq"`
	NodeID   uint64 `gorm:"index;not null" json:"node_id"`
	SpaceID  uint64 `gorm:"index;not null" json:"space_id"`
	BlobHash string `gorm:"size:64;index" json:"blob_hash,omitempty"`
	Name     string `gorm:"size:255" json:"name"`
	Path     string `gorm:"size:1024" json:"path"`
	IsDir    bool   `gorm:"not null" json:"is_dir"`
	// DeletedBy 为 0 表示由系统清理任务删除。
	DeletedBy uint64    `gorm:"not null;default:0" json:"deleted_by"`
	CreatedAt time.Time `gorm:"index" json:"deleted_at"`
}

// TableName 指定表名。
func (NodeTombstone) TableName() string { return "ly_node_tombstone" }

// 知识库文档的索引状态。
const (
	KBPending  = "pending"  // 排队等待索引
	KBIndexing = "indexing" // 正在抽取或向量化
	KBDone     = "done"     // 已建好索引
	KBSkipped  = "skipped"  // 不是能抽出文字的类型，或抽出来是空的（扫描件）
	KBFailed   = "failed"   // 抽取或向量化出错，详情在 Err
)

// KBDoc 是知识库里一份"内容"的索引档案。
//
// 注意主键语义是 BlobHash 而不是 NodeID：乐云是内容寻址存储，
// 同一份合同被三个部门各存一份时 NodeID 有三个、BlobHash 只有一个。
// 按内容建档，抽取与向量化就只做一次——文件越大、部门越多，省得越多。
// 至于"谁能看到这份内容"，那是检索时按 BlobHash 反查节点再过权限的事。
type KBDoc struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	BlobHash string `gorm:"size:64;uniqueIndex;not null" json:"blob_hash"`
	// Name/Ext 取首次见到这份内容时的文件名，仅用于展示与排错。
	Name   string `gorm:"size:255" json:"name"`
	Ext    string `gorm:"size:32" json:"ext"`
	Size   int64  `gorm:"not null;default:0" json:"size"`
	Status string `gorm:"size:16;index;not null" json:"status"`
	Chars  int    `gorm:"not null;default:0" json:"chars"`
	Chunks int    `gorm:"not null;default:0" json:"chunks"`
	// Model/Dim 记录这份索引是用哪个向量模型、什么维度建的。
	// 换模型后旧向量与新查询不在同一个空间里，比对出来的相似度没有意义，
	// 所以换模型必须整体重建，靠这两个字段识别。
	Model string `gorm:"size:64" json:"model"`
	Dim   int    `gorm:"not null;default:0" json:"dim"`
	Err   string `gorm:"size:512" json:"err,omitempty"`
	// Attempts 记录失败重试次数，连续失败的文档不再无限重试。
	Attempts  int        `gorm:"not null;default:0" json:"attempts"`
	IndexedAt *time.Time `json:"indexed_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (KBDoc) TableName() string { return "ly_kb_doc" }

// KBChunk 是一个文本块及其向量。
type KBChunk struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	BlobHash string `gorm:"size:64;index;not null" json:"blob_hash"`
	Seq      int    `gorm:"not null" json:"seq"`
	Text     string `gorm:"type:text" json:"text"`
	// Vector 是 float32 小端序的裸字节，且已做 L2 归一化——
	// 归一化之后余弦相似度就等于点积，检索时少一遍开方和除法。
	Vector    []byte    `gorm:"type:blob" json:"-"`
	Dim       int       `gorm:"not null" json:"dim"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (KBChunk) TableName() string { return "ly_kb_chunk" }

// KBConversation 是一轮问答会话。会话属于发起人，别人看不到。
type KBConversation struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint64    `gorm:"index;not null" json:"user_id"`
	Title     string    `gorm:"size:128" json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `gorm:"index" json:"updated_at"`
}

// TableName 指定表名。
func (KBConversation) TableName() string { return "ly_kb_conversation" }

// KBMessage 是会话里的一条消息。
type KBMessage struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	ConvID  uint64 `gorm:"index;not null" json:"conv_id"`
	Role    string `gorm:"size:16;not null" json:"role"` // user / assistant
	Content string `gorm:"type:text" json:"content"`
	// Citations 是引用到的文档，JSON 数组。存的是当时这个人有权看到的那些节点，
	// 不重新计算——权限后来变了不该改写历史记录里显示过什么。
	Citations string    `gorm:"type:text" json:"citations,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (KBMessage) TableName() string { return "ly_kb_message" }

// Setting 是运行期可改的系统配置项（键值对）。
//
// 列名特意避开 key/value：二者在 MySQL 中是保留字，用原名会逼着每条 SQL 都加反引号，
// 而反引号在 PostgreSQL 下又不合法，直接断了多数据库支持。
type Setting struct {
	Key       string    `gorm:"column:config_key;primaryKey;size:64" json:"key"`
	Value     string    `gorm:"column:config_value;type:text" json:"value"`
	Remark    string    `gorm:"size:255" json:"remark"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Setting) TableName() string { return "ly_setting" }

// AllModels 返回需要自动迁移的全部实体。
func AllModels() []any {
	return []any{
		&Department{}, &User{}, &Space{}, &Node{}, &Blob{},
		&AccessRule{}, &Share{}, &ShareTarget{}, &UploadSession{},
		&AuditLog{}, &Setting{}, &APIKey{}, &NodeTombstone{},
		&KBDoc{}, &KBChunk{}, &KBConversation{}, &KBMessage{},
	}
}
