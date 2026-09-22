package model

import (
	"sort"
	"strings"
)

// Permission 是权限位掩码。授权、拒绝、以及最终生效的权限都用它表示。
type Permission uint32

// 权限位定义。企业网盘的最小可用集合：能不能看见、能不能拿走、能不能改、能不能再授权。
const (
	// PermView 浏览目录、查看文件信息与在线预览。
	PermView Permission = 1 << iota
	// PermDownload 下载原文件（可与 PermView 分离，做到"可看不可下"）。
	PermDownload
	// PermUpload 上传文件、新建子目录。
	PermUpload
	// PermEdit 重命名、移动、覆盖更新。
	PermEdit
	// PermDelete 删除到回收站以及彻底删除。
	PermDelete
	// PermShare 创建对外分享链接。
	PermShare
	// PermManage 管理该目录的权限（授权/取消授权）。
	PermManage
)

// PermNone 与 PermAll 是两个常用的边界值。
const (
	PermNone Permission = 0
	PermAll             = PermView | PermDownload | PermUpload | PermEdit | PermDelete | PermShare | PermManage
)

// 常用权限组合，便于前端一键选择。
const (
	// PermReadOnly 只读：可浏览可下载。
	PermReadOnly = PermView | PermDownload
	// PermWrite 读写：可浏览下载上传编辑删除，但不能分享出去，也不能授权。
	// 适合"资料只能在部门内流转"的目录。
	PermWrite = PermReadOnly | PermUpload | PermEdit | PermDelete
	// PermCollaborate 协作：在读写之上再放开分享。部门空间默认用它——
	// 同事之间连发个内部链接都做不到的共享盘没人会用；
	// 对外公开分享另有系统级开关把关。
	PermCollaborate = PermWrite | PermShare
	// PermFull 完全控制。
	PermFull = PermAll
)

var permNames = []struct {
	perm Permission
	code string
	desc string
}{
	{PermView, "view", "查看"},
	{PermDownload, "download", "下载"},
	{PermUpload, "upload", "上传"},
	{PermEdit, "edit", "编辑"},
	{PermDelete, "delete", "删除"},
	{PermShare, "share", "分享"},
	{PermManage, "manage", "授权管理"},
}

// Has 判断是否包含 want 中的全部权限位。
func (p Permission) Has(want Permission) bool { return p&want == want }

// HasAny 判断是否包含 want 中的任意一个权限位。
func (p Permission) HasAny(want Permission) bool { return p&want != 0 }

// Codes 把位掩码展开成稳定顺序的权限码，用于 API 输出。
func (p Permission) Codes() []string {
	out := make([]string, 0, len(permNames))
	for _, item := range permNames {
		if p.Has(item.perm) {
			out = append(out, item.code)
		}
	}
	return out
}

// String 返回逗号分隔的权限码，主要用于日志。
func (p Permission) String() string {
	codes := p.Codes()
	if len(codes) == 0 {
		return "none"
	}
	return strings.Join(codes, ",")
}

// ParsePermissions 把权限码列表解析成位掩码，未知的权限码会被忽略。
func ParsePermissions(codes []string) Permission {
	var p Permission
	for _, c := range codes {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "view":
			p |= PermView
		case "download":
			p |= PermDownload
		case "upload":
			p |= PermUpload
		case "edit":
			p |= PermEdit
		case "delete":
			p |= PermDelete
		case "share":
			p |= PermShare
		case "manage":
			p |= PermManage
		case "all", "full":
			p |= PermAll
		case "read", "readonly":
			p |= PermReadOnly
		case "write":
			p |= PermWrite
		case "collaborate":
			p |= PermCollaborate
		}
	}
	return p
}

// Normalize 补齐权限之间的隐含依赖：任何一种操作都以"能看见"为前提，
// 授权管理者必然拥有全部业务权限，否则会出现"能改权限却打不开目录"的死角。
func (p Permission) Normalize() Permission {
	if p == PermNone {
		return PermNone
	}
	if p.Has(PermManage) {
		return PermAll
	}
	return p | PermView
}

// PermissionOption 是一条权限说明，供前端渲染勾选框。
type PermissionOption struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Value uint32 `json:"value"`
}

// PermissionCatalog 返回全部权限项。
func PermissionCatalog() []PermissionOption {
	out := make([]PermissionOption, 0, len(permNames))
	for _, item := range permNames {
		out = append(out, PermissionOption{Code: item.code, Label: item.desc, Value: uint32(item.perm)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}
