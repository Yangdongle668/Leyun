package service

import (
	"strings"
	"testing"

	"github.com/Yangdongle668/Leyun/internal/store"
)

// TestAllSafeHidesSecrets 钉死"秘密不出服务层"。
//
// 这条曾经是真实漏洞：管理页面直接把 All() 的结果发给浏览器，
// 令牌签名密钥也在里面。拿到它就能伪造任意账号（含超管）的登录令牌，
// 改密和停用都拦不住，只有轮换密钥才能止血。
func TestAllSafeHidesSecrets(t *testing.T) {
	db := newTestDB(t)
	s := NewSettingService(db)

	const jwtSecret = "acd20fa87f1a307ab142e9cf0b811bcbb85325f467752fae998c2eca5879"
	const apiKey = "sk-proj-0123456789abcdefWXYZ"
	mustSeed(t, s, store.SettingJWTSecret, jwtSecret)
	mustSeed(t, s, store.SettingAIAPIKey, apiKey)
	mustSeed(t, s, store.SettingSiteName, "乐云企业网盘")

	safe, err := s.AllSafe()
	if err != nil {
		t.Fatalf("AllSafe 失败: %v", err)
	}

	for _, secret := range []string{jwtSecret, apiKey} {
		for k, v := range safe {
			if strings.Contains(v, secret) {
				t.Fatalf("配置项 %s 把秘密原样带出来了: %s", k, v)
			}
		}
	}
	if got := safe[store.SettingJWTSecret]; got != "****5879" {
		t.Fatalf("令牌密钥掩码不对: %q", got)
	}
	if got := safe[store.SettingAIAPIKey]; got != "****WXYZ" {
		t.Fatalf("API Key 掩码不对: %q", got)
	}
	// 非秘密项必须照常可读，否则管理页面就没得显示了。
	if got := safe[store.SettingSiteName]; got != "乐云企业网盘" {
		t.Fatalf("普通配置被误伤: %q", got)
	}

	// 服务内部仍要拿得到真值，否则签名和调模型都做不了。
	if got := s.Get(store.SettingAIAPIKey, ""); got != apiKey {
		t.Fatalf("服务内部读不到真实密钥: %q", got)
	}
}

// TestMaskedSecretWriteIsIgnored 前端把掩码原样提交回来时不能覆盖真实密钥。
//
// 管理页面上 API Key 显示为 ****WXYZ，管理员只改了站点名就点保存，
// 整个表单会连掩码一起提交。这里不挡住的话，真实密钥就被 "****WXYZ" 覆盖了。
func TestMaskedSecretWriteIsIgnored(t *testing.T) {
	db := newTestDB(t)
	s := NewSettingService(db)

	const apiKey = "sk-proj-0123456789abcdefWXYZ"
	mustSeed(t, s, store.SettingAIAPIKey, apiKey)

	if err := s.SetMany(map[string]string{
		store.SettingSiteName: "新名字",
		store.SettingAIAPIKey: MaskSecret(apiKey),
	}); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	if got := s.Get(store.SettingAIAPIKey, ""); got != apiKey {
		t.Fatalf("真实密钥被掩码覆盖了: %q", got)
	}
	if got := s.Get(store.SettingSiteName, ""); got != "新名字" {
		t.Fatalf("同批次的普通配置没写进去: %q", got)
	}

	// 换成真的新值要能写进去。
	if err := s.SetMany(map[string]string{store.SettingAIAPIKey: "sk-new-value-1234"}); err != nil {
		t.Fatalf("更新密钥失败: %v", err)
	}
	if got := s.Get(store.SettingAIAPIKey, ""); got != "sk-new-value-1234" {
		t.Fatalf("新密钥没生效: %q", got)
	}

	// 明确传空串表示清空。
	if err := s.SetMany(map[string]string{store.SettingAIAPIKey: ""}); err != nil {
		t.Fatalf("清空密钥失败: %v", err)
	}
	if got := s.Get(store.SettingAIAPIKey, "zzz"); got != "" {
		t.Fatalf("密钥没被清空: %q", got)
	}
}

// TestSetRejectsUnknownKey 白名单之外的键一律拒绝，避免前端写入任意配置。
func TestSetRejectsUnknownKey(t *testing.T) {
	db := newTestDB(t)
	s := NewSettingService(db)

	if err := s.Set("system.totally_made_up", "1"); err == nil {
		t.Fatal("白名单外的配置项竟然写成功了")
	}
	// SetMany 对未知键是静默跳过（批量保存时不该因为一个陌生键整体失败）。
	if err := s.SetMany(map[string]string{"system.totally_made_up": "1"}); err != nil {
		t.Fatalf("SetMany 不该因未知键报错: %v", err)
	}
	if got := s.Get("system.totally_made_up", "none"); got != "none" {
		t.Fatalf("未知键竟然被写进去了: %q", got)
	}
}

// mustSeed 直接落库，绕开白名单——模拟系统自己写入的配置。
func mustSeed(t *testing.T, s *SettingService, key, value string) {
	t.Helper()
	if err := s.forceSet(key, value); err != nil {
		t.Fatalf("预置配置 %s 失败: %v", key, err)
	}
}
