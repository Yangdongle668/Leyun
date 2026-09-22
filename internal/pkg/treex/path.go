// Package treex 提供物化路径（materialized path）的拼装与解析。
//
// 部门树与目录树都用 "/1/4/9/" 这种格式保存祖先链：
//   - 判断"是否在某棵子树下"只要一次 LIKE 前缀匹配；
//   - 取全部祖先 ID 不需要递归查询。
package treex

import (
	"strconv"
	"strings"
)

// Root 是根路径。
const Root = "/"

// Build 由父路径与自身 ID 拼出本节点路径。
func Build(parentPath string, id uint64) string {
	if parentPath == "" {
		parentPath = Root
	}
	if !strings.HasSuffix(parentPath, "/") {
		parentPath += "/"
	}
	return parentPath + strconv.FormatUint(id, 10) + "/"
}

// Parent 返回父路径；根路径的父路径仍是根路径。
func Parent(path string) string {
	ids := IDs(path)
	if len(ids) <= 1 {
		return Root
	}
	return Prefix(ids[:len(ids)-1])
}

// Prefix 把一串 ID 拼回路径。
func Prefix(ids []uint64) string {
	if len(ids) == 0 {
		return Root
	}
	var sb strings.Builder
	sb.WriteByte('/')
	for _, id := range ids {
		sb.WriteString(strconv.FormatUint(id, 10))
		sb.WriteByte('/')
	}
	return sb.String()
}

// IDs 解析路径中的全部 ID（含自身，顺序由根到叶）。
func IDs(path string) []uint64 {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if id, err := strconv.ParseUint(p, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// AncestorIDs 返回不含自身的祖先 ID 列表。
func AncestorIDs(path string) []uint64 {
	ids := IDs(path)
	if len(ids) == 0 {
		return nil
	}
	return ids[:len(ids)-1]
}

// Depth 返回节点深度，根目录（"/"）为 0。
func Depth(path string) int { return len(IDs(path)) }

// SelfID 返回路径末端的 ID，根路径返回 0。
func SelfID(path string) uint64 {
	ids := IDs(path)
	if len(ids) == 0 {
		return 0
	}
	return ids[len(ids)-1]
}

// IsDescendant 判断 child 是否位于 ancestor 子树内（含自身）。
func IsDescendant(child, ancestor string) bool {
	if ancestor == "" || ancestor == Root {
		return true
	}
	if !strings.HasSuffix(ancestor, "/") {
		ancestor += "/"
	}
	return strings.HasPrefix(child, ancestor)
}

// LikePrefix 返回用于 SQL LIKE 的子树前缀，例如 "/1/4/" -> "/1/4/%"。
//
// 调用方需要自行用参数绑定传入，避免把用户输入直接拼进 SQL。
func LikePrefix(path string) string {
	if path == "" {
		path = Root
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path + "%"
}

// Rebase 把 path 中的 oldPrefix 段替换为 newPrefix，用于整棵子树搬家。
func Rebase(path, oldPrefix, newPrefix string) string {
	if !strings.HasSuffix(oldPrefix, "/") {
		oldPrefix += "/"
	}
	if !strings.HasSuffix(newPrefix, "/") {
		newPrefix += "/"
	}
	if !strings.HasPrefix(path, oldPrefix) {
		return path
	}
	return newPrefix + strings.TrimPrefix(path, oldPrefix)
}
