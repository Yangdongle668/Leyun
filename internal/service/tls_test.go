package service

import (
	"strings"
	"testing"

	"github.com/Yangdongle668/Leyun/internal/store"
)

func TestParseDomains(t *testing.T) {
	cases := map[string][]string{
		"pan.example.com":                    {"pan.example.com"},
		"pan.example.com, drive.example.com": {"pan.example.com", "drive.example.com"},
		"pan.example.com\ndrive.example.com": {"pan.example.com", "drive.example.com"},
		"  PAN.Example.COM  ":                {"pan.example.com"},
		"https://pan.example.com/":           {"pan.example.com"},
		"http://pan.example.com:8080":        {"pan.example.com"},
		"pan.example.com.":                   {"pan.example.com"},
		"pan.example.com,pan.example.com":    {"pan.example.com"},
		"pan.example.com;drive.example.com":  {"pan.example.com", "drive.example.com"},
		"":                                   {},
		"   ":                                {},
	}
	for in, want := range cases {
		got := ParseDomains(in)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("ParseDomains(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestValidateDomainRejectsUnissuable 申请不下来的域名要当场说清楚。
//
// 不挡的话，管理员点了申请要等几十秒，再收到一句看不懂的英文 ACME 报错。
func TestValidateDomainRejectsUnissuable(t *testing.T) {
	bad := []string{
		"", "localhost", "192.168.1.10", "10.0.0.1", "::1",
		"pan.local", "nas.internal", "drive.lan", "foo.test",
		"pan example.com", "pan..example.com",
	}
	for _, d := range bad {
		if err := ValidateDomain(d); err == nil {
			t.Errorf("%q 应当被拒绝", d)
		}
	}
	good := []string{
		"pan.example.com", "drive.company.cn", "a.b.c.d.example.org",
		"xn--fiqs8s.example.com", "云盘.中国",
	}
	for _, d := range good {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("%q 应当被接受，实际: %v", d, err)
		}
	}
}

// TestManagerNeedsAllPreconditions 三个前提缺一不可，否则宁可不启用，
// 也不要进入"开了但不生效"这种最难排查的状态。
func TestManagerNeedsAllPreconditions(t *testing.T) {
	db := newTestDB(t)
	setting := NewSettingService(db)
	svc := NewTLSService(setting, t.TempDir())

	if svc.Manager() != nil {
		t.Fatal("什么都没配就返回了管理器")
	}

	must := func(k, v string) {
		t.Helper()
		if err := setting.Set(k, v); err != nil {
			t.Fatalf("设置 %s 失败: %v", k, err)
		}
	}
	must(store.SettingTLSEnabled, "true")
	if svc.Manager() != nil {
		t.Fatal("没填域名就返回了管理器")
	}
	must(store.SettingTLSDomains, "pan.example.com")
	if svc.Manager() != nil {
		t.Fatal("没同意服务条款就返回了管理器——ACME 要求明示同意")
	}
	must(store.SettingTLSAgreedAt, "2026-09-22T00:00:00Z")
	if svc.Manager() == nil {
		t.Fatal("前提齐了却没返回管理器")
	}
}

// TestHostPolicyOnlyAllowsBoundDomains 是这里最要紧的一条。
//
// 不限制域名的话，任何人把自己的域名解析到这台机器，就能让它去替对方申请证书，
// 很快会撞上签发机构的频率限额，把真正要用的域名一起卡住。
func TestHostPolicyOnlyAllowsBoundDomains(t *testing.T) {
	db := newTestDB(t)
	setting := NewSettingService(db)
	svc := NewTLSService(setting, t.TempDir())

	for k, v := range map[string]string{
		store.SettingTLSEnabled:  "true",
		store.SettingTLSDomains:  "pan.example.com, drive.example.com",
		store.SettingTLSAgreedAt: "2026-09-22T00:00:00Z",
	} {
		if err := setting.Set(k, v); err != nil {
			t.Fatalf("设置 %s 失败: %v", k, err)
		}
	}
	mgr := svc.Manager()
	if mgr == nil {
		t.Fatal("没拿到管理器")
	}

	for _, ok := range []string{"pan.example.com", "drive.example.com", "PAN.EXAMPLE.COM"} {
		if err := mgr.HostPolicy(t.Context(), ok); err != nil {
			t.Errorf("%s 应当放行，实际: %v", ok, err)
		}
	}
	for _, bad := range []string{"evil.com", "pan.example.com.evil.com", "sub.pan.example.com", ""} {
		if err := mgr.HostPolicy(t.Context(), bad); err == nil {
			t.Errorf("%s 应当被拒绝——否则别人能拿我们的机器去刷证书", bad)
		}
	}
}

// TestManagerRebuildsOnConfigChange 后台改了域名不该还要重启才生效。
func TestManagerRebuildsOnConfigChange(t *testing.T) {
	db := newTestDB(t)
	setting := NewSettingService(db)
	svc := NewTLSService(setting, t.TempDir())

	for k, v := range map[string]string{
		store.SettingTLSEnabled:  "true",
		store.SettingTLSDomains:  "pan.example.com",
		store.SettingTLSAgreedAt: "2026-09-22T00:00:00Z",
	} {
		if err := setting.Set(k, v); err != nil {
			t.Fatalf("设置失败: %v", err)
		}
	}
	first := svc.Manager()
	if first == nil {
		t.Fatal("没拿到管理器")
	}
	// 配置没变时要复用同一个实例，否则每次请求都重建，缓存全白费。
	if svc.Manager() != first {
		t.Fatal("配置没变却重建了管理器")
	}

	if err := setting.Set(store.SettingTLSDomains, "pan.example.com,new.example.com"); err != nil {
		t.Fatalf("改域名失败: %v", err)
	}
	second := svc.Manager()
	if second == first {
		t.Fatal("改了域名却没重建管理器")
	}
	if err := second.HostPolicy(t.Context(), "new.example.com"); err != nil {
		t.Fatalf("新加的域名没生效: %v", err)
	}

	// 关掉开关要立刻不再提供管理器。
	if err := setting.Set(store.SettingTLSEnabled, "false"); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	if svc.Manager() != nil {
		t.Fatal("关掉之后还在返回管理器")
	}
}

// TestStatusDoesNotTriggerIssuance 状态页会被反复刷新，
// 绝不能每刷一次就去打一次签发机构——那很容易撞上频率限额。
func TestStatusDoesNotTriggerIssuance(t *testing.T) {
	db := newTestDB(t)
	setting := NewSettingService(db)
	dir := t.TempDir()
	svc := NewTLSService(setting, dir)

	for k, v := range map[string]string{
		store.SettingTLSEnabled:  "true",
		store.SettingTLSDomains:  "pan.example.com",
		store.SettingTLSAgreedAt: "2026-09-22T00:00:00Z",
		// 指一个必然连不通的地址：真去申请就会超时，测试会明显变慢。
		store.SettingTLSDirectoryURL: "http://127.0.0.1:1/directory",
	} {
		if err := setting.Set(k, v); err != nil {
			t.Fatalf("设置失败: %v", err)
		}
	}

	st := svc.Status()
	if !st.Enabled || len(st.Certs) != 1 {
		t.Fatalf("状态不对: %+v", st)
	}
	if st.Certs[0].Issued {
		t.Fatal("还没申请过就说已签发")
	}
	if !st.Staging {
		t.Fatal("填了自定义目录地址却没标记为测试环境")
	}
}

func TestFriendlyACMEError(t *testing.T) {
	cases := map[string]string{
		"acme: dns problem: NXDOMAIN looking up A for pan.example.com":                              "解析",
		"Get \"http://pan.example.com/.well-known/acme-challenge/x\": dial tcp: connection refused": "端口",
		"acme: urn:ietf:params:acme:error:rateLimited: too many certificates":                       "频率",
	}
	for raw, want := range cases {
		got := friendlyACMEError("pan.example.com", errString(raw))
		if !strings.Contains(got, want) {
			t.Errorf("翻译 %q 时缺少关键词 %q，实际: %s", raw, want, got)
		}
		// 原始信息要保留，方便真的要深究时有据可查。
		if !strings.Contains(got, raw) {
			t.Errorf("翻译时把原始信息丢了: %s", got)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
