package model

import "testing"

func TestPermissionHasAndCodes(t *testing.T) {
	p := PermView | PermDownload
	if !p.Has(PermView) {
		t.Fatalf("应当包含 view")
	}
	if p.Has(PermUpload) {
		t.Fatalf("不应包含 upload")
	}
	if !p.HasAny(PermUpload | PermView) {
		t.Fatalf("HasAny 应当命中 view")
	}
	codes := p.Codes()
	if len(codes) != 2 || codes[0] != "view" || codes[1] != "download" {
		t.Fatalf("权限码顺序应稳定，实际 %v", codes)
	}
}

func TestParsePermissions(t *testing.T) {
	cases := []struct {
		in   []string
		want Permission
	}{
		{[]string{"view", "download"}, PermReadOnly},
		{[]string{"VIEW", " Download "}, PermReadOnly},
		{[]string{"read"}, PermReadOnly},
		{[]string{"write"}, PermWrite},
		{[]string{"collaborate"}, PermCollaborate},
		{[]string{"all"}, PermAll},
		{[]string{"unknown"}, PermNone},
		{nil, PermNone},
	}
	for _, c := range cases {
		if got := ParsePermissions(c.in); got != c.want {
			t.Errorf("ParsePermissions(%v) = %s, 期望 %s", c.in, got, c.want)
		}
	}
}

func TestPermissionNormalize(t *testing.T) {
	// 任何操作都以能看见为前提。
	if got := PermDownload.Normalize(); !got.Has(PermView) {
		t.Errorf("只给下载权时应自动补上查看权，实际 %s", got)
	}
	// 能改权限的人必然拥有全部业务权限，否则会出现"能授权却打不开目录"。
	if got := PermManage.Normalize(); got != PermAll {
		t.Errorf("授权管理应展开为全部权限，实际 %s", got)
	}
	// 空权限保持为空，不能凭空长出查看权。
	if got := PermNone.Normalize(); got != PermNone {
		t.Errorf("空权限应保持为空，实际 %s", got)
	}
}

func TestPermCollaborateIncludesShare(t *testing.T) {
	// 部门空间默认用 PermCollaborate，成员必须能发内部分享。
	if !PermCollaborate.Has(PermShare) {
		t.Fatalf("协作权限必须包含分享")
	}
	if PermCollaborate.Has(PermManage) {
		t.Fatalf("协作权限不应包含授权管理")
	}
	if PermWrite.Has(PermShare) {
		t.Fatalf("纯读写权限不应包含分享")
	}
}

func TestPermissionCatalogCoversAllBits(t *testing.T) {
	var union Permission
	for _, item := range PermissionCatalog() {
		union |= Permission(item.Value)
	}
	if union != PermAll {
		t.Fatalf("权限目录应覆盖全部权限位，实际 %s", union)
	}
}

func TestRoleValidity(t *testing.T) {
	for _, r := range []Role{RoleSuperAdmin, RoleDeptAdmin, RoleMember} {
		if !r.Valid() {
			t.Errorf("%s 应为合法角色", r)
		}
		if r.Label() == "" {
			t.Errorf("%s 缺少中文名", r)
		}
	}
	if Role("root").Valid() {
		t.Errorf("未知角色不应通过校验")
	}
}
