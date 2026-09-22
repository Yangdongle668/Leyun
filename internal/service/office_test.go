package service

import (
	"testing"

	"github.com/Yangdongle668/Leyun/internal/config"
)

func TestIsOfficeDocument(t *testing.T) {
	yes := []string{"方案.docx", "报表.xlsx", "汇报.pptx", "说明.odt", "数据.csv", "README.txt", "旧版.doc"}
	for _, name := range yes {
		if !IsOfficeDocument(name) {
			t.Errorf("%s 应当可以用在线 Office 打开", name)
		}
	}
	no := []string{"图片.png", "归档.zip", "视频.mp4", "无扩展名", "代码.go"}
	for _, name := range no {
		if IsOfficeDocument(name) {
			t.Errorf("%s 不应被当作 Office 文档", name)
		}
	}
}

func TestOfficeEditableFormats(t *testing.T) {
	// 能原样存回去的才开放编辑；旧二进制格式转存会丢格式，只给只读预览。
	for _, ext := range []string{"docx", "xlsx", "pptx", "odt", "ods", "odp", "pdf"} {
		if !officeEditableExts[ext] {
			t.Errorf("%s 应当可编辑", ext)
		}
	}
	for _, ext := range []string{"doc", "xls", "ppt", "rtf", "djvu", "xps"} {
		if officeEditableExts[ext] {
			t.Errorf("%s 只应提供只读预览", ext)
		}
	}
}

func TestOfficeDocumentTypeMapping(t *testing.T) {
	cases := map[string]string{
		"docx": "word", "odt": "word", "txt": "word",
		"xlsx": "cell", "csv": "cell", "ods": "cell",
		"pptx": "slide", "odp": "slide",
		"pdf": "pdf", "djvu": "pdf", "xps": "pdf", "oxps": "pdf",
	}
	for ext, want := range cases {
		if got := officeDocTypes[ext]; got != want {
			t.Errorf("%s 应映射到 %s，实际 %s", ext, want, got)
		}
	}
}

func TestIsPDFLike(t *testing.T) {
	// 这些走 PDF 链路：默认用内置阅读器打开，要改内容才转 ONLYOFFICE。
	for _, name := range []string{"说明书.pdf", "扫描件.PDF", "古籍.djvu", "打印稿.xps"} {
		if !IsPDFLike(name) {
			t.Errorf("%s 应当走 PDF 链路", name)
		}
	}
	for _, name := range []string{"方案.docx", "报表.xlsx", "图片.png", "无扩展名"} {
		if IsPDFLike(name) {
			t.Errorf("%s 不应走 PDF 链路", name)
		}
	}
	// PDF 同时也算"能用在线 Office 打开"，编辑入口才会出现。
	if !IsOfficeDocument("说明书.pdf") {
		t.Errorf("PDF 也应能用在线 Office 打开")
	}
}

func TestPDFEditToggle(t *testing.T) {
	cfg := config.Default()
	cfg.Office.Enabled = true
	cfg.Office.PDFEdit = true
	svc := &OfficeService{cfg: cfg}
	if !svc.PDFEditEnabled() {
		t.Errorf("开启后应当允许 PDF 在线编辑")
	}

	// 关掉开关是给 8.1 之前的 Document Server 留的退路。
	cfg.Office.PDFEdit = false
	if svc.PDFEditEnabled() {
		t.Errorf("关闭后不应允许 PDF 在线编辑")
	}

	// Office 整体没开时，PDF 编辑当然也不可用（内置阅读器不受影响）。
	cfg.Office.Enabled = false
	cfg.Office.PDFEdit = true
	if svc.PDFEditEnabled() {
		t.Errorf("未启用 Office 时不应允许 PDF 在线编辑")
	}
}

func TestPDFEditDefaultsOn(t *testing.T) {
	// compose 固定的 ONLYOFFICE 8.2 支持 PDF 编辑，默认就该开着。
	if !config.Default().Office.PDFEdit {
		t.Errorf("PDF 在线编辑应当默认开启")
	}
}

// 回调里的文件地址来自外部服务，直接拿去请求等于把后端变成 SSRF 跳板。
func TestCallbackURLHostAllowlist(t *testing.T) {
	cfg := config.Default()
	cfg.Office.PublicURL = "http://pan.corp.com:8081"
	cfg.Office.InternalURL = "http://onlyoffice"
	svc := &OfficeService{cfg: cfg}

	allowed := []string{
		"onlyoffice",        // 内网服务名，完全一致
		"onlyoffice:80",     // 回调常带上内部端口
		"pan.corp.com:8081", // 对外地址
		"pan.corp.com:9999", // 同主机不同端口，仍是同一个服务
		"ONLYOFFICE",        // 主机名大小写不敏感
	}
	for _, host := range allowed {
		if !svc.allowedHost(host) {
			t.Errorf("主机 %q 应被放行", host)
		}
	}

	denied := []string{
		"127.0.0.1:8080",      // 回连自己
		"169.254.169.254",     // 云元数据服务
		"evil.com",            // 完全无关的外部主机
		"onlyoffice.evil.com", // 前缀伪装
		"",                    // 空主机
	}
	for _, host := range denied {
		if svc.allowedHost(host) {
			t.Errorf("主机 %q 应被拒绝", host)
		}
	}
}

func TestFetchEditedFileRejectsUntrustedURL(t *testing.T) {
	cfg := config.Default()
	cfg.Office.PublicURL = "http://onlyoffice:80"
	cfg.Office.InternalURL = "http://onlyoffice:80"
	svc := &OfficeService{cfg: cfg}

	bad := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/api/v1/admin/users",
		"file:///etc/passwd",
		"gopher://onlyoffice/x",
		"://broken",
	}
	for _, raw := range bad {
		if _, err := svc.fetchEditedFile(raw); err == nil {
			t.Errorf("地址 %q 应被拒绝", raw)
		}
	}
}

func TestShortHash(t *testing.T) {
	// 文档 key 必须随内容变化，否则 Document Server 会拿缓存里的旧版本。
	if got := shortHash("abcdef0123456789"); got != "abcdef0123" {
		t.Errorf("应截取前 10 位，实际 %s", got)
	}
	if got := shortHash(""); got != "new" {
		t.Errorf("空哈希应有占位值，实际 %s", got)
	}
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("短哈希应原样返回，实际 %s", got)
	}
}

func TestParseUint(t *testing.T) {
	if got := parseUint("42"); got != 42 {
		t.Errorf("应解析为 42，实际 %d", got)
	}
	// Document Server 回传的 users 里可能是任意字符串，非数字要安全地退化为 0。
	for _, bad := range []string{"", "abc", "1a", "-1", "3.14"} {
		if got := parseUint(bad); got != 0 {
			t.Errorf("%q 应解析为 0，实际 %d", bad, got)
		}
	}
}

func TestOfficeConfigValidation(t *testing.T) {
	cfg := config.Default()
	cfg.Office.Enabled = true
	cfg.Office.PublicURL = ""
	if _, err := config.Load(""); err != nil {
		t.Fatalf("默认配置应当可用: %v", err)
	}
	// 开了在线编辑却没配地址，应当在启动时就拦住，而不是等用户点开文档才报错。
	t.Setenv("LEYUN_OFFICE_ENABLED", "true")
	if _, err := config.Load(""); err == nil {
		t.Errorf("开启 Office 但未配置地址时应当报错")
	}
}
