package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/storage"
)

// 上传撞上同名文件时的三条路：保留两者、替换、跳过。
//
// 这里直接打 UploadService.SimpleUpload，因为要验的恰恰是
// "解析重名 → 开事务 → 写节点" 这一整串，单测 CreateFile 反而盖不住
// PlanConflict 必须在事务外跑这个约束。

func newUploadEnv(t *testing.T) (*testEnv, *UploadService, *FileService) {
	t.Helper()
	env := newTestEnv(t)
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Storage.Root = filepath.Join(dir, "blobs")
	cfg.Storage.TempRoot = filepath.Join(dir, "tmp")
	cfg.Storage.ChunkSize = 8 << 20
	store, err := storage.New(cfg.Storage.Root, cfg.Storage.TempRoot)
	if err != nil {
		t.Fatalf("初始化存储失败: %v", err)
	}
	space := NewSpaceService(env.db, env.acl)
	file := NewFileService(env.db, cfg, store, env.acl, space)
	return env, NewUploadService(env.db, cfg, store, file, env.acl, space), file
}

func upload(t *testing.T, up *UploadService, subj *Subject, spaceID uint64, name, body string, mode ConflictMode) (*model.Node, error) {
	t.Helper()
	return up.SimpleUpload(subj, spaceID, 0, name, int64(len(body)), strings.NewReader(body), mode)
}

func TestUploadConflictKeepBoth(t *testing.T) {
	env, up, _ := newUploadEnv(t)
	subj := env.subject(t, env.admin)

	first, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第一版", ConflictRename)
	if err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}
	second, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第二版", ConflictRename)
	if err != nil {
		t.Fatalf("重名上传失败: %v", err)
	}

	if second.ID == first.ID {
		t.Fatal("保留两者应当新建一个节点，而不是盖掉原来的")
	}
	if second.Name != "报价单(1).txt" {
		t.Errorf("新文件应改名成 报价单(1).txt，实际 %q", second.Name)
	}
	// 老文件必须原封不动。
	var old model.Node
	if err := env.db.First(&old, first.ID).Error; err != nil {
		t.Fatalf("读取原文件失败: %v", err)
	}
	if old.Version != 1 || old.BlobHash != first.BlobHash {
		t.Errorf("原文件不该被动过，version=%d hash=%s", old.Version, old.BlobHash)
	}
}

func TestUploadConflictReplace(t *testing.T) {
	env, up, _ := newUploadEnv(t)
	subj := env.subject(t, env.admin)

	first, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第一版", ConflictRename)
	if err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}
	second, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第二版内容更长一些", ConflictReplace)
	if err != nil {
		t.Fatalf("覆盖上传失败: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("覆盖应当写回同一个节点：原 %d，现在 %d", first.ID, second.ID)
	}
	if second.Version != 2 {
		t.Errorf("覆盖后版本号应为 2，实际 %d", second.Version)
	}
	if second.BlobHash == first.BlobHash {
		t.Error("覆盖后内容哈希不该和原来一样")
	}
	// 目录里只该有一个文件，不该多出 报价单(1).txt。
	var count int64
	env.db.Model(&model.Node{}).
		Where("space_id = ? AND parent_id = ? AND trashed = ?", env.publicSpace.ID, 0, false).
		Count(&count)
	if count != 1 {
		t.Errorf("覆盖后目录里应只剩 1 个文件，实际 %d 个", count)
	}
	// 用量要按差值调整，不能把两份都算进去。
	var sp model.Space
	if err := env.db.First(&sp, env.publicSpace.ID).Error; err != nil {
		t.Fatalf("读取空间失败: %v", err)
	}
	if sp.UsedBytes != second.Size {
		t.Errorf("空间用量应等于当前文件大小 %d，实际 %d", second.Size, sp.UsedBytes)
	}
}

func TestUploadConflictReplaceNeedsEditPermission(t *testing.T) {
	env, up, _ := newUploadEnv(t)
	admin := env.subject(t, env.admin)
	if _, err := upload(t, up, admin, env.publicSpace.ID, "报价单.txt", "第一版", ConflictRename); err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}

	// 李四只有查看 + 上传：能往里放文件，但不该能改掉别人已有的文件。
	env.grant(t, &model.AccessRule{
		SpaceID: env.publicSpace.ID, PrincipalType: model.PrincipalUser,
		PrincipalID: env.li.ID, Allow: model.PermView | model.PermUpload, Inheritable: true,
	})
	li := env.subject(t, env.li)

	_, err := upload(t, up, li, env.publicSpace.ID, "报价单.txt", "偷偷改掉", ConflictReplace)
	if err == nil {
		t.Fatal("没有编辑权限却覆盖成功了")
	}
	var be *response.Error
	if !errors.As(err, &be) || be.Code != response.CodeForbidden {
		t.Fatalf("应当返回 403，实际 %v", err)
	}
	// 而且不能退而求其次悄悄存成 报价单(1).txt——那样用户会以为自己更新成功了。
	var count int64
	env.db.Model(&model.Node{}).
		Where("space_id = ? AND name LIKE ?", env.publicSpace.ID, "报价单%").Count(&count)
	if count != 1 {
		t.Errorf("被拒绝后不该留下任何新文件，实际有 %d 个", count)
	}

	// 但只要不选覆盖，他照样可以正常上传一份自己的。
	if _, err := upload(t, up, li, env.publicSpace.ID, "报价单.txt", "我自己的", ConflictRename); err != nil {
		t.Fatalf("保留两者应当允许：%v", err)
	}
}

func TestUploadConflictSkipReportsConflict(t *testing.T) {
	env, up, _ := newUploadEnv(t)
	subj := env.subject(t, env.admin)
	if _, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第一版", ConflictRename); err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}
	_, err := upload(t, up, subj, env.publicSpace.ID, "报价单.txt", "第二版", ConflictSkip)
	if err == nil {
		t.Fatal("选了跳过却还是传上去了")
	}
	var be *response.Error
	if !errors.As(err, &be) || be.Code != response.CodeConflict {
		t.Fatalf("应当返回 409，实际 %v", err)
	}
	// 不重名的时候跳过模式不该拦着。
	if _, err := upload(t, up, subj, env.publicSpace.ID, "另一个.txt", "内容", ConflictSkip); err != nil {
		t.Fatalf("不重名时跳过模式应当照常上传：%v", err)
	}
}

func TestUploadConflictReplaceFallsBackWhenNameIsDir(t *testing.T) {
	env, up, file := newUploadEnv(t)
	subj := env.subject(t, env.admin)
	if _, err := file.Mkdir(subj, env.publicSpace.ID, 0, "资料"); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}

	// 文件不能盖掉同名文件夹，只能退回加序号。
	node, err := upload(t, up, subj, env.publicSpace.ID, "资料", "我是文件不是目录", ConflictReplace)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if node.IsDir {
		t.Fatal("上传出来的不该是目录")
	}
	if node.Name != "资料(1)" {
		t.Errorf("应改名成 资料(1)，实际 %q", node.Name)
	}
}

// 覆盖上传要在只有一条数据库连接时也能跑完。
//
// 线上 SQLite 是 SetMaxOpenConns(1)：事务一开就把唯一那条连接占住了，
// 这时候在事务里再发一条查询（比如去查覆盖权限）会永远等不到连接。
// 所以 PlanConflict 必须在事务外先跑完——这个用例就是盯着这件事的，
// 真写回事务里的话它会直接超时，而不是悄悄在生产上卡死。
func TestUploadConflictReplaceWithSingleConnection(t *testing.T) {
	env, up, _ := newUploadEnv(t)
	sqlDB, err := env.db.DB()
	if err != nil {
		t.Fatalf("取底层连接池失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	subj := env.subject(t, env.admin)
	if _, err := upload(t, up, subj, env.publicSpace.ID, "规格书.txt", "第一版", ConflictRename); err != nil {
		t.Fatalf("首次上传失败: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := upload(t, up, subj, env.publicSpace.ID, "规格书.txt", "第二版", ConflictReplace)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("单连接下覆盖上传失败: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("单连接下覆盖上传卡住了——多半是在事务里又发了一条查询")
	}
}

func TestParseConflictMode(t *testing.T) {
	cases := map[string]ConflictMode{
		"replace": ConflictReplace,
		"REPLACE": ConflictReplace,
		" skip ":  ConflictSkip,
		"rename":  ConflictRename,
		"":        ConflictRename,
		"乱写":      ConflictRename,
	}
	for in, want := range cases {
		if got := ParseConflictMode(in); got != want {
			t.Errorf("ParseConflictMode(%q) = %q，期望 %q", in, got, want)
		}
	}
}
