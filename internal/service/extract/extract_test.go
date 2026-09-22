package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestPlainTextUTF8(t *testing.T) {
	const body = "第一章 授权对象\n授权可以指定到人、指定到部门。\n"
	got := mustExtract(t, []byte(body), "txt")
	if !strings.Contains(got, "指定到部门") {
		t.Fatalf("正文丢了: %q", got)
	}
}

// TestPlainTextGBK 是国内企业网盘绕不开的场景：
// Windows 记事本默认存 ANSI（简体中文环境下就是 GBK），
// 不转码的话整份文件抽出来是乱码，既污染索引又白烧向量额度。
func TestPlainTextGBK(t *testing.T) {
	const body = "乐云企业网盘的权限沿部门树继承。"
	gbk, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(body))
	if err != nil {
		t.Fatalf("构造 GBK 样本失败: %v", err)
	}
	if bytes.Equal(gbk, []byte(body)) {
		t.Fatal("样本没有真的变成 GBK，测试无意义")
	}
	got := mustExtract(t, gbk, "txt")
	if got != body {
		t.Fatalf("GBK 转码不对:\n want %q\n got  %q", body, got)
	}
}

func TestPlainTextUTF8BOM(t *testing.T) {
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte("带 BOM 的文本")...)
	got := mustExtract(t, body, "txt")
	if got != "带 BOM 的文本" {
		t.Fatalf("BOM 没被去掉: %q", got)
	}
}

func TestDocx(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>年度技术规划</w:t></w:r></w:p>
<w:p><w:r><w:t>第一节 </w:t></w:r><w:r><w:t>目标</w:t></w:r></w:p>
<w:p><w:r><w:t>把权限模型讲清楚。</w:t></w:r></w:p>
</w:body></w:document>`
	data := buildZip(t, map[string]string{"word/document.xml": doc})
	got := mustExtract(t, data, "docx")

	for _, want := range []string{"年度技术规划", "目标", "把权限模型讲清楚。"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q，实际:\n%s", want, got)
		}
	}
	// 同一段里被拆成多个 run 的文字要拼回去，不能被段落分隔符劈开。
	if !strings.Contains(got, "第一节 目标") {
		t.Fatalf("同段内的多个 run 没拼好:\n%s", got)
	}
	// 段落之间必须断开，否则切块时会把两段粘成一句。
	if strings.Contains(got, "年度技术规划第一节") {
		t.Fatalf("段落之间没有断开:\n%s", got)
	}
}

// TestXlsxSharedStrings 钉住 Excel 的共享字符串表。
// 表格里的字符串大多存在 sharedStrings.xml 里，工作表格子里只有下标；
// 不去查表的话抽出来会是一堆数字。
func TestXlsxSharedStrings(t *testing.T) {
	shared := `<?xml version="1.0"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="3" uniqueCount="3">
<si><t>部门</t></si><si><t>配额</t></si><si><t>研发中心</t></si>
</sst>`
	sheet := `<?xml version="1.0"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>107374182400</v></c></row>
</sheetData></worksheet>`
	data := buildZip(t, map[string]string{
		"xl/sharedStrings.xml":     shared,
		"xl/worksheets/sheet1.xml": sheet,
	})
	got := mustExtract(t, data, "xlsx")

	for _, want := range []string{"部门", "配额", "研发中心", "107374182400"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q，实际:\n%s", want, got)
		}
	}
	// 下标不能被当成内容本身抽出来。
	if strings.Contains(got, "\t0\t") || strings.HasPrefix(got, "0") {
		t.Fatalf("共享字符串的下标被当成内容了:\n%s", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != 2 {
		t.Fatalf("应当是两行，实际 %d 行:\n%s", len(lines), got)
	}
}

func TestPptx(t *testing.T) {
	slide := `<?xml version="1.0"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
<p:cSld><p:spTree><p:sp><p:txBody>
<a:p><a:r><a:t>乐云企业网盘</a:t></a:r></a:p>
<a:p><a:r><a:t>分部门权限管理</a:t></a:r></a:p>
</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	notes := `<?xml version="1.0"?>
<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
         xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
<a:p><a:r><a:t>备注：记得讲切断继承</a:t></a:r></a:p></p:notes>`
	data := buildZip(t, map[string]string{
		"ppt/slides/slide1.xml":           slide,
		"ppt/notesSlides/notesSlide1.xml": notes,
	})
	got := mustExtract(t, data, "pptx")

	for _, want := range []string{"乐云企业网盘", "分部门权限管理", "记得讲切断继承"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q，实际:\n%s", want, got)
		}
	}
}

// TestUnsupportedAndEmpty 区分"不该抽"和"抽不出来"。
//
// 这两种都不是故障，但必须分开：前者是图片、压缩包这类本来就没文字的；
// 后者最常见的是扫描件 PDF，管理员需要看到提示才知道那份合同其实没进知识库。
func TestUnsupportedAndEmpty(t *testing.T) {
	if _, err := Extract(bytes.NewReader([]byte("\x89PNG\r\n")), 6, "png"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("png 应当报不支持，实际: %v", err)
	}
	if _, err := Extract(bytes.NewReader([]byte("   \n\n  ")), 7, "txt"); !errors.Is(err, ErrNoText) {
		t.Fatalf("空白文件应当报没有文字，实际: %v", err)
	}
	if Supported("png") || Supported("zip") || Supported("exe") {
		t.Fatal("二进制类型不该被认为可抽取")
	}
	for _, ext := range []string{"txt", "MD", ".docx", "PDF", "xlsx"} {
		if !Supported(ext) {
			t.Fatalf("%s 应当被认为可抽取（扩展名要大小写与前导点都兼容）", ext)
		}
	}
}

// TestTidyCollapsesBlankLines 抽出来的文本常有大段空行（PDF、表格尤其明显），
// 不压掉会白白占满切块容量，把真正的内容挤出去。
func TestTidyCollapsesBlankLines(t *testing.T) {
	got := mustExtract(t, []byte("第一段   \n\n\n\n\n第二段\t\t\n\n\n第三段"), "txt")
	want := "第一段\n\n第二段\n\n第三段"
	if got != want {
		t.Fatalf("空白清理不对:\n want %q\n got  %q", want, got)
	}
}

// TestMalformedDocxDoesNotPanic 坏文件不能把索引任务带崩。
func TestMalformedDocxDoesNotPanic(t *testing.T) {
	for _, ext := range []string{"docx", "xlsx", "pptx", "pdf"} {
		junk := []byte("这不是一个合法的文档，只是一串随便的字节 \x00\x01\x02")
		if _, err := Extract(bytes.NewReader(junk), int64(len(junk)), ext); err == nil {
			t.Fatalf("%s: 畸形文件竟然抽成功了", ext)
		}
	}
}

func mustExtract(t *testing.T, data []byte, ext string) string {
	t.Helper()
	got, err := Extract(bytes.NewReader(data), int64(len(data)), ext)
	if err != nil {
		t.Fatalf("抽取 %s 失败: %v", ext, err)
	}
	return got
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写入 zip 部件 %s 失败: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("写入 zip 内容失败: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return buf.Bytes()
}
