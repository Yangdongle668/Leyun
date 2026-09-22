package service

import (
	"strings"
	"testing"
	"time"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
)

// newAIEnv 在 acl_test 的组织架构之上，补出 AI 接口需要的服务与若干文档。
type aiEnv struct {
	*testEnv
	ai   *AIService
	keys *APIKeyService
	// 平台组空间里的两份文件，张三能看、李四看不到。
	pfDoc1, pfDoc2 *model.Node
	// 市场部空间里的一份文件，反过来。
	mkDoc *model.Node
	// 公共空间里的一份文件，谁都能看。
	pubDoc *model.Node
}

func newAIEnv(t *testing.T) *aiEnv {
	t.Helper()
	base := newTestEnv(t)
	cfg := config.Default()
	space := NewSpaceService(base.db, base.acl)
	user := NewUserService(base.db, cfg, space, base.acl)
	env := &aiEnv{
		testEnv: base,
		ai:      NewAIService(base.db, base.acl, nil, space, user),
		keys:    NewAPIKeyService(base.db),
	}

	// 部门空间的默认授权：本部门（含子部门）可协作。
	for _, pair := range []struct {
		space *model.Space
		dept  *model.Department
	}{{base.pfSpace, base.pfDept}, {base.mkSpace, base.mkDept}} {
		env.grant(t, &model.AccessRule{
			SpaceID: pair.space.ID, PrincipalType: model.PrincipalDept,
			PrincipalID: pair.dept.ID, Allow: model.PermCollaborate,
			IncludeSubDept: true, Inheritable: true,
		})
	}
	env.grant(t, &model.AccessRule{
		SpaceID: base.publicSpace.ID, PrincipalType: model.PrincipalEveryone,
		Allow: model.PermReadOnly, Inheritable: true,
	})

	env.pfDoc1 = env.mkFile(t, base.pfSpace, "架构设计.docx", "hash-pf-1")
	env.pfDoc2 = env.mkFile(t, base.pfSpace, "接口清单.xlsx", "hash-pf-2")
	env.mkDoc = env.mkFile(t, base.mkSpace, "投放预算.xlsx", "hash-mk-1")
	env.pubDoc = env.mkFile(t, base.publicSpace, "员工手册.pdf", "hash-pub-1")
	return env
}

// mkFile 在空间根下建一个文件节点。
func (e *aiEnv) mkFile(t *testing.T, space *model.Space, name, hash string) *model.Node {
	t.Helper()
	n := &model.Node{
		SpaceID: space.ID, Name: name, IsDir: false,
		BlobHash: hash, Size: int64(len(name)) * 1024, Version: 1,
	}
	if err := e.db.Create(n).Error; err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	n.Path = "/" + itoa(n.ID) + "/"
	if err := e.db.Model(n).Update("path", n.Path).Error; err != nil {
		t.Fatalf("回写路径失败: %v", err)
	}
	return n
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

/* ---------------- API Key ---------------- */

func TestAPIKeyCreateAndAuthenticate(t *testing.T) {
	env := newAIEnv(t)
	created, err := env.keys.Create(env.admin, CreateKeyInput{
		Name:   "知识库索引",
		Scopes: []string{string(ScopeIndexRead), string(ScopeContentRead)},
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if !strings.HasPrefix(created.Key, KeyPrefix) {
		t.Errorf("密钥应带固定前缀，实际 %s", created.Key)
	}
	// 明文只出现这一次，库里存的必须是指纹而不是原文。
	if strings.Contains(created.Record.KeyHash, created.Key) {
		t.Errorf("数据库里不应出现明文密钥")
	}
	if created.Record.Prefix == created.Key {
		t.Errorf("展示用前缀不应等于完整密钥")
	}

	ctx, err := env.keys.Authenticate(created.Key, "10.0.0.1")
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !ctx.Has(ScopeIndexRead) || !ctx.Has(ScopeContentRead) {
		t.Errorf("应具备签发时勾选的能力")
	}
	if ctx.Has(ScopeACLCheck) {
		t.Errorf("没勾的能力不应凭空出现")
	}
	// 未限定空间的密钥对所有空间放行。
	if !ctx.AllowsSpace(env.pfSpace.ID) || !ctx.AllowsSpace(env.mkSpace.ID) {
		t.Errorf("未限定空间时应全部放行")
	}
}

func TestAPIKeyRejectsBadInput(t *testing.T) {
	env := newAIEnv(t)
	for _, raw := range []string{"", "not-a-key", KeyPrefix + "deadbeef", "Bearer x"} {
		if _, err := env.keys.Authenticate(raw, "10.0.0.1"); err == nil {
			t.Errorf("非法密钥 %q 应被拒绝", raw)
		}
	}
}

func TestAPIKeyOnlySuperAdminCanIssue(t *testing.T) {
	env := newAIEnv(t)
	// 一把能读全公司文件的钥匙，只能由超管签发。
	for _, u := range []*model.User{env.wang, env.zhang, env.li} {
		if _, err := env.keys.Create(u, CreateKeyInput{
			Name: "越权", Scopes: []string{string(ScopeIndexRead)},
		}); err == nil {
			t.Errorf("%s 不应能签发密钥", u.Username)
		}
	}
}

func TestAPIKeyDisableAndExpire(t *testing.T) {
	env := newAIEnv(t)
	created, err := env.keys.Create(env.admin, CreateKeyInput{
		Name: "临时", Scopes: []string{string(ScopeIndexRead)},
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	if err := env.keys.SetEnabled(created.Record.ID, false); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if _, err := env.keys.Authenticate(created.Key, "10.0.0.1"); err == nil {
		t.Errorf("停用后应立即失效")
	}
	if err := env.keys.SetEnabled(created.Record.ID, true); err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	if _, err := env.keys.Authenticate(created.Key, "10.0.0.1"); err != nil {
		t.Errorf("重新启用后应可用")
	}

	past := time.Now().Add(-time.Hour)
	if err := env.db.Model(created.Record).Update("expire_at", past).Error; err != nil {
		t.Fatalf("设置过期失败: %v", err)
	}
	if _, err := env.keys.Authenticate(created.Key, "10.0.0.1"); err == nil {
		t.Errorf("过期后应失效")
	}
}

func TestAPIKeySpaceRestriction(t *testing.T) {
	env := newAIEnv(t)
	// 只给公共空间的密钥：做"全员一份"的知识库时就该这么签。
	created, err := env.keys.Create(env.admin, CreateKeyInput{
		Name: "仅公共空间", Scopes: []string{string(ScopeIndexRead)},
		SpaceIDs: []uint64{env.publicSpace.ID},
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	ctx, err := env.keys.Authenticate(created.Key, "10.0.0.1")
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !ctx.AllowsSpace(env.publicSpace.ID) {
		t.Errorf("白名单内的空间应放行")
	}
	if ctx.AllowsSpace(env.pfSpace.ID) || ctx.AllowsSpace(env.mkSpace.ID) {
		t.Errorf("白名单外的空间应拒绝")
	}
}

/* ---------------- 枚举与增量 ---------------- */

func TestListDocumentsCursorCoversEverythingExactlyOnce(t *testing.T) {
	env := newAIEnv(t)
	// 造够多的文件，逼出多页。
	for i := 0; i < 25; i++ {
		env.mkFile(t, env.pfSpace, "批量-"+itoa(uint64(i))+".txt", "bulk-"+itoa(uint64(i)))
	}

	seen := map[uint64]int{}
	cursor := ""
	for page := 0; page < 20; page++ {
		res, err := env.ai.ListDocuments(DocumentQuery{Cursor: cursor, Limit: 7})
		if err != nil {
			t.Fatalf("枚举失败: %v", err)
		}
		for _, item := range res.Items {
			seen[item.NodeID]++
		}
		cursor = res.NextCursor
		if !res.HasMore {
			break
		}
	}

	var total int64
	if err := env.db.Model(&model.Node{}).Where("is_dir = ? AND trashed = ?", false, false).
		Count(&total).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if int64(len(seen)) != total {
		t.Errorf("游标翻页应不重不漏：拿到 %d 条，库里 %d 条", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("节点 %d 被返回了 %d 次", id, n)
		}
	}
}

func TestListDocumentsIncrementalPicksUpChanges(t *testing.T) {
	env := newAIEnv(t)
	// 先整体拉一遍，拿到末尾游标。
	cursor := ""
	for {
		res, err := env.ai.ListDocuments(DocumentQuery{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatalf("枚举失败: %v", err)
		}
		cursor = res.NextCursor
		if !res.HasMore {
			break
		}
	}
	after, err := env.ai.ListDocuments(DocumentQuery{Cursor: cursor, Limit: 50})
	if err != nil {
		t.Fatalf("增量查询失败: %v", err)
	}
	if len(after.Items) != 0 {
		t.Fatalf("没有变更时增量应为空，实际 %d 条", len(after.Items))
	}

	// 改一个文件的内容，增量必须能捞到它。
	time.Sleep(10 * time.Millisecond)
	err = env.db.Model(&model.Node{}).Where("id = ?", env.pfDoc1.ID).
		Updates(map[string]any{"blob_hash": "hash-pf-1-v2", "version": 2}).Error
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	after, err = env.ai.ListDocuments(DocumentQuery{Cursor: cursor, Limit: 50})
	if err != nil {
		t.Fatalf("增量查询失败: %v", err)
	}
	if len(after.Items) != 1 || after.Items[0].NodeID != env.pfDoc1.ID {
		t.Fatalf("增量应恰好捞到被改动的那一条，实际 %d 条", len(after.Items))
	}
	if after.Items[0].BlobHash != "hash-pf-1-v2" {
		t.Errorf("应返回新的内容哈希，实际 %s", after.Items[0].BlobHash)
	}
}

func TestListDocumentsExcludesTrashedByDefault(t *testing.T) {
	env := newAIEnv(t)
	now := time.Now()
	err := env.db.Model(&model.Node{}).Where("id = ?", env.pfDoc2.ID).
		Updates(map[string]any{"trashed": true, "trashed_at": now, "trash_root_id": env.pfDoc2.ID}).Error
	if err != nil {
		t.Fatalf("移入回收站失败: %v", err)
	}

	res, err := env.ai.ListDocuments(DocumentQuery{Limit: 100})
	if err != nil {
		t.Fatalf("枚举失败: %v", err)
	}
	for _, item := range res.Items {
		if item.NodeID == env.pfDoc2.ID {
			t.Errorf("默认不该返回回收站里的条目")
		}
	}

	// 显式要的时候才给，并且带上标记让索引程序知道该下架。
	res, err = env.ai.ListDocuments(DocumentQuery{Limit: 100, IncludeTrashed: true})
	if err != nil {
		t.Fatalf("枚举失败: %v", err)
	}
	var found bool
	for _, item := range res.Items {
		if item.NodeID == env.pfDoc2.ID {
			found = true
			if !item.Trashed {
				t.Errorf("回收站条目应带 trashed 标记")
			}
		}
	}
	if !found {
		t.Errorf("显式要求时应返回回收站条目")
	}
}

func TestListDocumentsReturnsReadablePath(t *testing.T) {
	env := newAIEnv(t)
	dir := env.mkNode(t, env.pfSpace, nil, "设计文档")
	sub := env.mkNode(t, env.pfSpace, dir, "2026")
	deep := &model.Node{SpaceID: env.pfSpace.ID, ParentID: sub.ID, Name: "评审记录.docx", BlobHash: "deep-1", Version: 1}
	if err := env.db.Create(deep).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	deep.Path = sub.Path + itoa(deep.ID) + "/"
	if err := env.db.Model(deep).Update("path", deep.Path).Error; err != nil {
		t.Fatalf("回写路径失败: %v", err)
	}

	res, err := env.ai.ListDocuments(DocumentQuery{Limit: 100})
	if err != nil {
		t.Fatalf("枚举失败: %v", err)
	}
	for _, item := range res.Items {
		if item.NodeID != deep.ID {
			continue
		}
		got := strings.Join(item.PathNames, "/")
		if got != "设计文档/2026/评审记录.docx" {
			t.Errorf("应还原出可读路径，实际 %q", got)
		}
		return
	}
	t.Errorf("没有找到目标文档")
}

func TestDeletionFeed(t *testing.T) {
	env := newAIEnv(t)
	page, err := env.ai.ListDeletions(0, 100, nil)
	if err != nil {
		t.Fatalf("查询删除流水失败: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("初始应没有删除记录")
	}

	// 模拟彻底删除：真删行 + 写墓碑（与 purgeSubtree 的行为一致）。
	stone := model.NodeTombstone{
		NodeID: env.mkDoc.ID, SpaceID: env.mkDoc.SpaceID,
		BlobHash: env.mkDoc.BlobHash, Name: env.mkDoc.Name, Path: env.mkDoc.Path,
	}
	if err := env.db.Create(&stone).Error; err != nil {
		t.Fatalf("写墓碑失败: %v", err)
	}
	if err := env.db.Delete(&model.Node{}, env.mkDoc.ID).Error; err != nil {
		t.Fatalf("删除节点失败: %v", err)
	}

	page, err = env.ai.ListDeletions(0, 100, nil)
	if err != nil {
		t.Fatalf("查询删除流水失败: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].NodeID != env.mkDoc.ID {
		t.Fatalf("应捞到那条删除记录，实际 %d 条", len(page.Items))
	}
	// 游标推进后不该重复拿到同一条。
	page2, err := env.ai.ListDeletions(page.NextCursor, 100, nil)
	if err != nil {
		t.Fatalf("查询删除流水失败: %v", err)
	}
	if len(page2.Items) != 0 {
		t.Errorf("游标推进后不应再返回旧记录")
	}
}

/* ---------------- 批量鉴权：这是整套东西的命门 ---------------- */

func TestAuthorizeFiltersCrossDepartment(t *testing.T) {
	env := newAIEnv(t)
	all := []uint64{env.pfDoc1.ID, env.pfDoc2.ID, env.mkDoc.ID, env.pubDoc.ID}

	// 平台组的张三：看得到平台组的两份 + 公共空间的一份，看不到市场部的。
	res, err := env.ai.Authorize(env.zhang.ID, all, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "张三可见", res.Allowed, env.pfDoc1.ID, env.pfDoc2.ID, env.pubDoc.ID)
	assertSet(t, "张三不可见", res.Denied, env.mkDoc.ID)

	// 市场部的李四：正好反过来。
	res, err = env.ai.Authorize(env.li.ID, all, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "李四可见", res.Allowed, env.mkDoc.ID, env.pubDoc.ID)
	assertSet(t, "李四不可见", res.Denied, env.pfDoc1.ID, env.pfDoc2.ID)
}

func TestAuthorizeMatchesEffectiveExactly(t *testing.T) {
	env := newAIEnv(t)
	// 批量判定与逐个判定必须完全一致，否则会出现
	// "检索说能看、点开却打不开"，或者更糟——反过来。
	docs := []*model.Node{env.pfDoc1, env.pfDoc2, env.mkDoc, env.pubDoc}
	ids := make([]uint64, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}

	for _, u := range []*model.User{env.admin, env.zhang, env.li, env.wang} {
		res, err := env.ai.Authorize(u.ID, ids, model.PermView)
		if err != nil {
			t.Fatalf("鉴权失败: %v", err)
		}
		allowed := map[uint64]bool{}
		for _, id := range res.Allowed {
			allowed[id] = true
		}
		for _, d := range docs {
			space, err := env.ai.space.Get(d.SpaceID)
			if err != nil {
				t.Fatalf("加载空间失败: %v", err)
			}
			want := env.effective(t, u, space, d).Has(model.PermView)
			if allowed[d.ID] != want {
				t.Errorf("%s 对 %s：批量=%v 逐个=%v", u.Username, d.Name, allowed[d.ID], want)
			}
		}
	}
}

func TestAuthorizeRespectsDenyAndIsolation(t *testing.T) {
	env := newAIEnv(t)
	// 显式拒绝：整个平台组可读，唯独张三这一份不行。
	env.grant(t, &model.AccessRule{
		SpaceID: env.pfSpace.ID, NodeID: env.pfDoc1.ID,
		PrincipalType: model.PrincipalUser, PrincipalID: env.zhang.ID,
		Deny: model.PermView, Inheritable: true,
	})
	res, err := env.ai.Authorize(env.zhang.ID, []uint64{env.pfDoc1.ID, env.pfDoc2.ID}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "被拒的那份", res.Denied, env.pfDoc1.ID)
	assertSet(t, "其余仍可见", res.Allowed, env.pfDoc2.ID)

	// 切断继承：目录不再接收上层授权。
	secret := env.mkNode(t, env.pfSpace, nil, "薪酬")
	inside := &model.Node{SpaceID: env.pfSpace.ID, ParentID: secret.ID, Name: "明细.xlsx", BlobHash: "secret-1", Version: 1}
	if err := env.db.Create(inside).Error; err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	inside.Path = secret.Path + itoa(inside.ID) + "/"
	if err := env.db.Model(inside).Update("path", inside.Path).Error; err != nil {
		t.Fatalf("回写路径失败: %v", err)
	}
	if err := env.acl.SetInheritance(secret, false); err != nil {
		t.Fatalf("切断继承失败: %v", err)
	}

	res, err = env.ai.Authorize(env.zhang.ID, []uint64{inside.ID}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "切断继承后不可见", res.Denied, inside.ID)
}

func TestAuthorizeDistinguishesViewFromDownload(t *testing.T) {
	env := newAIEnv(t)
	// 公共空间只给了只读，市场部空间给了协作。
	res, err := env.ai.Authorize(env.li.ID, []uint64{env.pubDoc.ID, env.mkDoc.ID}, model.PermDownload)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "两份都可下载", res.Allowed, env.pubDoc.ID, env.mkDoc.ID)

	// 把公共空间改成"可看不可下"，再判一次。
	if err := env.db.Model(&model.AccessRule{}).
		Where("space_id = ? AND principal_type = ?", env.publicSpace.ID, model.PrincipalEveryone).
		Update("allow", model.PermView).Error; err != nil {
		t.Fatalf("改授权失败: %v", err)
	}
	res, err = env.ai.Authorize(env.li.ID, []uint64{env.pubDoc.ID}, model.PermDownload)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "不可下载", res.Denied, env.pubDoc.ID)

	// 但"能看见"仍然成立。
	res, err = env.ai.Authorize(env.li.ID, []uint64{env.pubDoc.ID}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	assertSet(t, "仍可查看", res.Allowed, env.pubDoc.ID)
}

func TestAuthorizeDeniesDisabledUser(t *testing.T) {
	env := newAIEnv(t)
	if err := env.db.Model(env.zhang).Update("status", model.UserDisabled).Error; err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	// 离职交接期最容易在这里出事：账号停了，知识库还在替他答。
	res, err := env.ai.Authorize(env.zhang.ID, []uint64{env.pfDoc1.ID, env.pubDoc.ID}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	if len(res.Allowed) != 0 {
		t.Errorf("已停用的账号不该看到任何东西，实际可见 %d 条", len(res.Allowed))
	}
}

func TestAuthorizeDeniesTrashed(t *testing.T) {
	env := newAIEnv(t)
	err := env.db.Model(&model.Node{}).Where("id = ?", env.pfDoc1.ID).
		Updates(map[string]any{"trashed": true, "trash_root_id": env.pfDoc1.ID}).Error
	if err != nil {
		t.Fatalf("移入回收站失败: %v", err)
	}
	res, err := env.ai.Authorize(env.zhang.ID, []uint64{env.pfDoc1.ID, env.pfDoc2.ID}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	// 权限够也不算"能看到"：索引里那条该下架了。
	assertSet(t, "回收站里的不可见", res.Denied, env.pfDoc1.ID)
	assertSet(t, "其余仍可见", res.Allowed, env.pfDoc2.ID)
}

func TestAuthorizeReportsMissingNodes(t *testing.T) {
	env := newAIEnv(t)
	res, err := env.ai.Authorize(env.zhang.ID, []uint64{env.pfDoc1.ID, 999999}, model.PermView)
	if err != nil {
		t.Fatalf("鉴权失败: %v", err)
	}
	// 明确告诉索引程序这条已经没了，而不是笼统地算作"无权限"。
	assertSet(t, "已消失的节点", res.Missing, 999999)
	assertSet(t, "仍在的节点", res.Allowed, env.pfDoc1.ID)
}

func TestAuthorizeRejectsOversizedBatch(t *testing.T) {
	env := newAIEnv(t)
	ids := make([]uint64, 1001)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	if _, err := env.ai.Authorize(env.zhang.ID, ids, model.PermView); err == nil {
		t.Errorf("超大批量应被拒绝")
	}
}

func TestAuthorizeEmptyInput(t *testing.T) {
	env := newAIEnv(t)
	res, err := env.ai.Authorize(env.zhang.ID, nil, model.PermView)
	if err != nil {
		t.Fatalf("空输入不应报错: %v", err)
	}
	// 返回空数组而不是 null，省得调用方在 JSON 里踩空指针。
	if res.Allowed == nil || res.Denied == nil || res.Missing == nil {
		t.Errorf("空结果也应返回空数组而不是 null")
	}
}

/* ---------------- 辅助 ---------------- */

func assertSet(t *testing.T, label string, got []uint64, want ...uint64) {
	t.Helper()
	gotSet := map[uint64]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	if len(got) != len(want) {
		t.Errorf("%s：期望 %v，实际 %v", label, want, got)
		return
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Errorf("%s：缺少 %d（实际 %v）", label, id, got)
		}
	}
}
