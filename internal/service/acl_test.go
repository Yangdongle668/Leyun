package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// testEnv 是一套完整的测试组织架构，贯穿本文件的所有用例。
//
//	总公司
//	├── 研发中心        王五（部门管理员）
//	│   └── 平台组      张三
//	└── 市场部          李四
type testEnv struct {
	db                        *gorm.DB
	acl                       *ACLService
	rootDept, rdDept          *model.Department
	pfDept, mkDept            *model.Department
	admin, zhang, li, wang    *model.User
	pfSpace, mkSpace          *model.Space
	publicSpace, zhangPrivate *model.Space
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	return db
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := newTestDB(t)
	env := &testEnv{db: db, acl: NewACLService(db)}

	mkDept := func(name string, parent *model.Department) *model.Department {
		d := &model.Department{Name: name, Enabled: true}
		parentPath := treex.Root
		if parent != nil {
			d.ParentID = parent.ID
			d.Depth = parent.Depth + 1
			parentPath = parent.Path
		}
		if err := db.Create(d).Error; err != nil {
			t.Fatalf("创建部门 %s 失败: %v", name, err)
		}
		d.Path = treex.Build(parentPath, d.ID)
		if err := db.Model(d).Update("path", d.Path).Error; err != nil {
			t.Fatalf("回写部门路径失败: %v", err)
		}
		return d
	}
	env.rootDept = mkDept("总公司", nil)
	env.rdDept = mkDept("研发中心", env.rootDept)
	env.pfDept = mkDept("平台组", env.rdDept)
	env.mkDept = mkDept("市场部", env.rootDept)

	mkUser := func(username string, role model.Role, dept *model.Department) *model.User {
		u := &model.User{
			Username: username, PasswordHash: "x", Nickname: username,
			Role: role, Status: model.UserActive, DeptID: dept.ID,
		}
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("创建用户 %s 失败: %v", username, err)
		}
		return u
	}
	env.admin = mkUser("admin", model.RoleSuperAdmin, env.rootDept)
	env.zhang = mkUser("zhangsan", model.RoleMember, env.pfDept)
	env.li = mkUser("lisi", model.RoleMember, env.mkDept)
	env.wang = mkUser("wangwu", model.RoleDeptAdmin, env.rdDept)

	mkSpace := func(sp *model.Space) *model.Space {
		if err := db.Create(sp).Error; err != nil {
			t.Fatalf("创建空间失败: %v", err)
		}
		return sp
	}
	env.pfSpace = mkSpace(&model.Space{Type: model.SpaceDepartment, Name: "平台组", DeptID: env.pfDept.ID, Enabled: true})
	env.mkSpace = mkSpace(&model.Space{Type: model.SpaceDepartment, Name: "市场部", DeptID: env.mkDept.ID, Enabled: true})
	env.publicSpace = mkSpace(&model.Space{Type: model.SpacePublic, Name: "公共空间", Enabled: true})
	env.zhangPrivate = mkSpace(&model.Space{Type: model.SpacePersonal, Name: "张三的空间", OwnerID: env.zhang.ID, Enabled: true})
	return env
}

func (e *testEnv) subject(t *testing.T, u *model.User) *Subject {
	t.Helper()
	subj, err := e.acl.LoadSubject(u)
	if err != nil {
		t.Fatalf("装配权限主体失败: %v", err)
	}
	return subj
}

func (e *testEnv) grant(t *testing.T, rule *model.AccessRule) *model.AccessRule {
	t.Helper()
	if err := e.db.Create(rule).Error; err != nil {
		t.Fatalf("写入权限规则失败: %v", err)
	}
	return rule
}

// mkNode 在空间里建一个目录节点。
func (e *testEnv) mkNode(t *testing.T, space *model.Space, parent *model.Node, name string) *model.Node {
	t.Helper()
	n := &model.Node{SpaceID: space.ID, Name: name, IsDir: true}
	parentPath := treex.Root
	if parent != nil {
		n.ParentID = parent.ID
		n.Depth = parent.Depth + 1
		parentPath = parent.Path
	}
	if err := e.db.Create(n).Error; err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}
	n.Path = treex.Build(parentPath, n.ID)
	if err := e.db.Model(n).Update("path", n.Path).Error; err != nil {
		t.Fatalf("回写节点路径失败: %v", err)
	}
	return n
}

func (e *testEnv) effective(t *testing.T, u *model.User, space *model.Space, node *model.Node) model.Permission {
	t.Helper()
	perm, err := e.acl.Effective(e.subject(t, u), space, node)
	if err != nil {
		t.Fatalf("计算权限失败: %v", err)
	}
	return perm
}

func TestSuperAdminHasEverything(t *testing.T) {
	env := newTestEnv(t)
	// 超管没有任何授权记录，但对任何空间都应当是全权。
	for _, sp := range []*model.Space{env.pfSpace, env.mkSpace, env.publicSpace, env.zhangPrivate} {
		if got := env.effective(t, env.admin, sp, nil); got != model.PermAll {
			t.Errorf("超管在 %s 上应有全部权限，实际 %s", sp.Name, got)
		}
	}
}

func TestPersonalSpaceIsolation(t *testing.T) {
	env := newTestEnv(t)
	if got := env.effective(t, env.zhang, env.zhangPrivate, nil); got != model.PermAll {
		t.Errorf("本人对个人空间应有全部权限，实际 %s", got)
	}
	// 别人的个人空间默认完全不可见。
	if got := env.effective(t, env.li, env.zhangPrivate, nil); got != model.PermNone {
		t.Errorf("他人个人空间默认应不可见，实际 %s", got)
	}
	// 张三显式把个人空间开给李四之后才可见。
	env.grant(t, &model.AccessRule{
		SpaceID: env.zhangPrivate.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermReadOnly, Inheritable: true,
	})
	got := env.effective(t, env.li, env.zhangPrivate, nil)
	if !got.Has(model.PermView) || got.Has(model.PermDelete) {
		t.Errorf("授权后李四应只读，实际 %s", got)
	}
}

func TestDepartmentGrantReachesSubDepartments(t *testing.T) {
	env := newTestEnv(t)
	// 授权给研发中心并勾选"包含子部门"，平台组的张三应当命中。
	rule := env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.rdDept.ID, Allow: model.PermReadOnly,
		IncludeSubDept: true, Inheritable: true,
	})
	if got := env.effective(t, env.zhang, env.pfSpace, nil); !got.Has(model.PermDownload) {
		t.Errorf("勾选含子部门时下级成员应命中，实际 %s", got)
	}

	// 取消"包含子部门"后，张三（在子部门）就不该再命中了。
	if err := env.db.Model(rule).Update("include_sub_dept", false).Error; err != nil {
		t.Fatalf("更新规则失败: %v", err)
	}
	if got := env.effective(t, env.zhang, env.pfSpace, nil); got != model.PermNone {
		t.Errorf("未勾选含子部门时下级成员不应命中，实际 %s", got)
	}

	// 但研发中心的直属成员王五仍然命中（这里用普通成员身份验证，排除部门管理员的特权）。
	wangAsMember := *env.wang
	wangAsMember.Role = model.RoleMember
	if got := env.effective(t, &wangAsMember, env.pfSpace, nil); !got.Has(model.PermView) {
		t.Errorf("直属部门成员应始终命中，实际 %s", got)
	}
}

func TestCrossDepartmentIsolation(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.pfDept.ID, Allow: model.PermCollaborate,
		IncludeSubDept: true, Inheritable: true,
	})
	if got := env.effective(t, env.zhang, env.pfSpace, nil); !got.Has(model.PermUpload) {
		t.Errorf("本部门成员应可上传，实际 %s", got)
	}
	// 市场部的李四与平台组毫无关系，应当完全看不到。
	if got := env.effective(t, env.li, env.pfSpace, nil); got != model.PermNone {
		t.Errorf("跨部门默认应不可见，实际 %s", got)
	}
}

func TestDenyBeatsAllow(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.mkDept.ID, Allow: model.PermReadOnly,
		IncludeSubDept: true, Inheritable: true,
	})
	if got := env.effective(t, env.li, env.pfSpace, nil); !got.Has(model.PermDownload) {
		t.Fatalf("前置条件失败：李四应先拿到只读权，实际 %s", got)
	}
	// "整个部门可读，唯独某人除外"是企业里的常见诉求。
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Deny: model.PermDownload, Inheritable: true,
	})
	got := env.effective(t, env.li, env.pfSpace, nil)
	if got.Has(model.PermDownload) {
		t.Errorf("显式拒绝应当压过部门授权，实际 %s", got)
	}
	if !got.Has(model.PermView) {
		t.Errorf("只拒下载不应连查看一起拒掉，实际 %s", got)
	}
}

func TestInheritanceDownDirectoryTree(t *testing.T) {
	env := newTestEnv(t)
	docs := env.mkNode(t, env.pfSpace, nil, "文档")
	secret := env.mkNode(t, env.pfSpace, docs, "密级")

	// 空间根上的可继承规则应当一路传到孙目录。
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.pfDept.ID, Allow: model.PermCollaborate,
		IncludeSubDept: true, Inheritable: true,
	})
	if got := env.effective(t, env.zhang, env.pfSpace, secret); !got.Has(model.PermUpload) {
		t.Errorf("可继承规则应传递到子目录，实际 %s", got)
	}

	// 在子目录上单独拒绝，只影响该子树。
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, NodeID: secret.ID,
		PrincipalType: model.PrincipalUser, PrincipalID: env.zhang.ID,
		Deny: model.PermView | model.PermDownload, Inheritable: true,
	})
	if got := env.effective(t, env.zhang, env.pfSpace, secret); got.Has(model.PermView) {
		t.Errorf("子目录上的拒绝规则未生效，实际 %s", got)
	}
	if got := env.effective(t, env.zhang, env.pfSpace, docs); !got.Has(model.PermView) {
		t.Errorf("子目录的拒绝不应影响父目录，实际 %s", got)
	}
}

func TestNonInheritableRuleStaysOnItsNode(t *testing.T) {
	env := newTestEnv(t)
	docs := env.mkNode(t, env.pfSpace, nil, "文档")
	child := env.mkNode(t, env.pfSpace, docs, "子目录")

	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, NodeID: docs.ID,
		PrincipalType: model.PrincipalUser, PrincipalID: env.li.ID,
		Allow: model.PermReadOnly, Inheritable: false,
	})
	if got := env.effective(t, env.li, env.pfSpace, docs); !got.Has(model.PermView) {
		t.Errorf("不可继承的规则在自身节点上必须生效，实际 %s", got)
	}
	if got := env.effective(t, env.li, env.pfSpace, child); got != model.PermNone {
		t.Errorf("不可继承的规则不应传给子目录，实际 %s", got)
	}
}

func TestExpiredRuleIsIgnored(t *testing.T) {
	env := newTestEnv(t)
	past := time.Now().Add(-time.Hour)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermReadOnly,
		Inheritable: true, ExpireAt: &past,
	})
	if got := env.effective(t, env.li, env.pfSpace, nil); got != model.PermNone {
		t.Errorf("过期规则不应再生效，实际 %s", got)
	}

	future := time.Now().Add(time.Hour)
	env.grant(t, &model.AccessRule{
		SpaceID: env.mkSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermReadOnly,
		Inheritable: true, ExpireAt: &future,
	})
	if got := env.effective(t, env.li, env.mkSpace, nil); !got.Has(model.PermView) {
		t.Errorf("未过期规则应生效，实际 %s", got)
	}
}

func TestEveryoneAndRoleGrants(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.publicSpace.ID, PrincipalType: model.PrincipalEveryone,
		Allow: model.PermReadOnly, Inheritable: true,
	})
	for _, u := range []*model.User{env.zhang, env.li, env.wang} {
		if got := env.effective(t, u, env.publicSpace, nil); !got.Has(model.PermDownload) {
			t.Errorf("%s 应能读公共空间，实际 %s", u.Username, got)
		}
	}
	// 再给部门管理员这个角色单独开放上传。
	env.grant(t, &model.AccessRule{
		SpaceID: env.publicSpace.ID, PrincipalType: model.PrincipalRole,
		PrincipalRole: model.RoleDeptAdmin, Allow: model.PermUpload, Inheritable: true,
	})
	if got := env.effective(t, env.wang, env.publicSpace, nil); !got.Has(model.PermUpload) {
		t.Errorf("部门管理员角色应拿到上传权，实际 %s", got)
	}
	if got := env.effective(t, env.zhang, env.publicSpace, nil); got.Has(model.PermUpload) {
		t.Errorf("普通成员不应拿到角色授权，实际 %s", got)
	}
}

func TestDepartmentAdminScope(t *testing.T) {
	env := newTestEnv(t)
	// 王五是研发中心的部门管理员，平台组在其子树内，应当全权。
	if got := env.effective(t, env.wang, env.pfSpace, nil); got != model.PermAll {
		t.Errorf("部门管理员应全权管理下级部门空间，实际 %s", got)
	}
	// 市场部是平级部门，不在其管辖范围。
	if got := env.effective(t, env.wang, env.mkSpace, nil); got != model.PermNone {
		t.Errorf("部门管理员不应染指平级部门，实际 %s", got)
	}
	// 部门管理员对别人的个人空间同样没有特权。
	if got := env.effective(t, env.wang, env.zhangPrivate, nil); got != model.PermNone {
		t.Errorf("部门管理员不应默认访问下属个人空间，实际 %s", got)
	}
}

func TestDisabledSpaceBlocksEveryoneButSuperAdmin(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.pfDept.ID, Allow: model.PermCollaborate,
		IncludeSubDept: true, Inheritable: true,
	})
	if err := env.db.Model(env.pfSpace).Update("enabled", false).Error; err != nil {
		t.Fatalf("停用空间失败: %v", err)
	}
	env.pfSpace.Enabled = false

	if got := env.effective(t, env.zhang, env.pfSpace, nil); got != model.PermNone {
		t.Errorf("空间停用后成员不应再有权限，实际 %s", got)
	}
	if got := env.effective(t, env.admin, env.pfSpace, nil); got != model.PermAll {
		t.Errorf("超管仍应能进入已停用空间处理善后，实际 %s", got)
	}
}

func TestEffectiveForChildrenMatchesEffective(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.pfDept.ID, Allow: model.PermCollaborate,
		IncludeSubDept: true, Inheritable: true,
	})
	open := env.mkNode(t, env.pfSpace, nil, "公开")
	locked := env.mkNode(t, env.pfSpace, nil, "锁定")
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, NodeID: locked.ID,
		PrincipalType: model.PrincipalUser, PrincipalID: env.zhang.ID,
		Deny: model.PermView, Inheritable: true,
	})

	subj := env.subject(t, env.zhang)
	parentPerm, err := env.acl.Effective(subj, env.pfSpace, nil)
	if err != nil {
		t.Fatalf("计算根目录权限失败: %v", err)
	}
	got, err := env.acl.EffectiveForChildren(subj, env.pfSpace, parentPerm, []model.Node{*open, *locked})
	if err != nil {
		t.Fatalf("批量计算子节点权限失败: %v", err)
	}
	// 批量版本必须与逐个计算的结果一致，否则列表里会出现"看得见点不开"。
	for _, n := range []*model.Node{open, locked} {
		want := env.effective(t, env.zhang, env.pfSpace, n)
		if got[n.ID] != want {
			t.Errorf("节点 %s 批量权限 %s 与逐个计算 %s 不一致", n.Name, got[n.ID], want)
		}
	}
	if got[locked.ID].Has(model.PermView) {
		t.Errorf("被拒绝的子目录不应出现查看权，实际 %s", got[locked.ID])
	}
}

func TestSaveRuleUpsertsInsteadOfDuplicating(t *testing.T) {
	env := newTestEnv(t)
	first := &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermReadOnly, Inheritable: true,
	}
	if err := env.acl.SaveRule(first); err != nil {
		t.Fatalf("首次授权失败: %v", err)
	}
	second := &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermCollaborate, Inheritable: true,
	}
	if err := env.acl.SaveRule(second); err != nil {
		t.Fatalf("再次授权失败: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("同一对象的重复授权应更新原记录，first=%d second=%d", first.ID, second.ID)
	}
	var count int64
	if err := env.db.Model(&model.AccessRule{}).
		Where("space_id = ? AND principal_type = ? AND principal_id = ?",
			env.pfSpace.ID, model.PrincipalUser, env.li.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计规则失败: %v", err)
	}
	if count != 1 {
		t.Errorf("期望只保留 1 条规则，实际 %d 条", count)
	}
	if got := env.effective(t, env.li, env.pfSpace, nil); !got.Has(model.PermUpload) {
		t.Errorf("更新后的权限未生效，实际 %s", got)
	}
}

func TestSaveRuleRejectsEmptyGrant(t *testing.T) {
	env := newTestEnv(t)
	err := env.acl.SaveRule(&model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser, PrincipalID: env.li.ID,
	})
	if err == nil {
		t.Fatalf("空权限的授权应当被拒绝")
	}
	err = env.acl.SaveRule(&model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalUser, Allow: model.PermReadOnly,
	})
	if err == nil {
		t.Fatalf("未指定授权对象时应当被拒绝")
	}
}

func TestAccessibleSpaceIDs(t *testing.T) {
	env := newTestEnv(t)
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, PrincipalType: model.PrincipalDept,
		PrincipalID: env.pfDept.ID, Allow: model.PermCollaborate,
		IncludeSubDept: true, Inheritable: true,
	})
	env.grant(t, &model.AccessRule{
		SpaceID: env.publicSpace.ID, PrincipalType: model.PrincipalEveryone,
		Allow: model.PermReadOnly, Inheritable: true,
	})

	ids, err := env.acl.AccessibleSpaceIDs(env.subject(t, env.zhang))
	if err != nil {
		t.Fatalf("查询可访问空间失败: %v", err)
	}
	set := map[uint64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	if !set[env.pfSpace.ID] || !set[env.publicSpace.ID] {
		t.Errorf("张三应能访问平台组与公共空间，实际 %v", ids)
	}
	if set[env.mkSpace.ID] {
		t.Errorf("张三不应出现在市场部空间的可访问列表里，实际 %v", ids)
	}
}
