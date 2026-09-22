// Package extract 从各类文档里抽出纯文本，供知识库建索引。
//
// 全部是纯 Go 实现：乐云的卖点之一是"单文件可执行程序"，
// 引入需要 CGO 或外部命令行工具（pdftotext、libreoffice）的方案，
// 会把这个卖点直接毁掉——部署时又得装一堆东西。
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// ErrUnsupported 表示这个类型压根不打算抽文字（图片、压缩包、可执行文件等）。
// 调用方应当把它记成"跳过"而不是"失败"——这不是错误，是本来就没有文字。
var ErrUnsupported = errors.New("该文件类型不支持提取文字")

// ErrNoText 表示类型支持，但实际没抽出任何文字。
//
// 最常见的来源是扫描件 PDF：整页都是图片，没有文字层。
// 这同样不是故障，但要单独报出来——管理员看到"某某文件没有文字"
// 才知道那份合同其实没进知识库，而不是以为索引好了却检索不到。
var ErrNoText = errors.New("文件里没有可提取的文字（可能是扫描件或空文件）")

// MaxOutput 单个文件最多抽多少字符。
//
// 设上限是因为下游要把文本切块再逐块调向量接口：一份 800 页的手册
// 能切出几千块，一次索引就可能把当月的接口额度烧掉一大截。
const MaxOutput = 2 << 20 // 约 200 万字符

// Supported 判断某个扩展名是否有可能抽出文字。
//
// 索引器用它来提前跳过，免得为了发现"这是张图片"而先把几十 MB 读进内存。
func Supported(ext string) bool {
	_, ok := extractors[normalizeExt(ext)]
	return ok
}

// Extensions 返回全部支持的扩展名，供界面上展示。
func Extensions() []string {
	out := make([]string, 0, len(extractors))
	for ext := range extractors {
		out = append(out, ext)
	}
	return out
}

type extractFunc func(r io.ReaderAt, size int64) (string, error)

var extractors map[string]extractFunc

func init() {
	// 放在 init 里而不是直接写字面量：plainText 等函数在包级变量初始化期
	// 会构成引用环，Go 会拒绝编译。
	extractors = map[string]extractFunc{
		"docx": docx,
		"xlsx": xlsx,
		"pptx": pptx,
		"pdf":  pdfText,
	}
	// 纯文本类共用一个实现。列成清单而不是"凡是不认识的都当文本读"，
	// 是为了避免把 exe、zip 这类二进制当文本塞进知识库。
	for _, ext := range []string{
		"txt", "md", "markdown", "csv", "tsv", "log", "json", "yaml", "yml",
		"xml", "html", "htm", "ini", "conf", "sql", "go", "java", "py", "js",
		"ts", "vue", "c", "h", "cpp", "cs", "php", "rb", "rs", "sh", "bat",
	} {
		extractors[ext] = plainText
	}
}

func normalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

// Extract 按扩展名抽取文本。
//
// 传 ReaderAt 而不是 Reader：zip 和 pdf 都需要随机读取，
// 用流式接口就只能先整个读进内存，大文件上会很难看。
func Extract(r io.ReaderAt, size int64, ext string) (string, error) {
	fn, ok := extractors[normalizeExt(ext)]
	if !ok {
		return "", ErrUnsupported
	}
	text, err := fn(r, size)
	if err != nil {
		return "", err
	}
	text = tidy(text)
	if strings.TrimSpace(text) == "" {
		return "", ErrNoText
	}
	if len(text) > MaxOutput {
		text = truncateRunes(text, MaxOutput)
	}
	return text, nil
}

// ===== 纯文本 =====

func plainText(r io.ReaderAt, size int64) (string, error) {
	if size > MaxOutput*4 {
		size = MaxOutput * 4
	}
	buf := make([]byte, size)
	n, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return decodeBytes(buf[:n])
}

// decodeBytes 把一段字节解成 UTF-8 字符串。
//
// 国内企业网盘里 GBK 编码的老文档非常多（Windows 记事本默认就是 ANSI）。
// 不做转码的话，这些文件抽出来全是乱码，既污染索引又浪费向量额度。
func decodeBytes(b []byte) (string, error) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), nil
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeWith(b, unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM))
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeWith(b, unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM))
	}
	if utf8.Valid(b) {
		return string(b), nil
	}
	// 不是合法 UTF-8，在中文环境里几乎一定是 GBK 系。
	// 用 GB18030 而不是 GBK：它是前者的超集，能多认繁体和少数民族文字，且兼容 GBK。
	s, err := decodeWith(b, simplifiedchinese.GB18030)
	if err != nil {
		// 转码也失败就按 UTF-8 强解，非法字节会变成替换符，
		// 总比整份文件丢掉强。
		return strings.ToValidUTF8(string(b), ""), nil
	}
	return s, nil
}

type decoder interface {
	NewDecoder() *encoding.Decoder
}

func decodeWith(b []byte, enc decoder) (string, error) {
	out, _, err := transform.Bytes(enc.NewDecoder(), b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ===== OOXML（docx / xlsx / pptx）=====

// zipParts 按前缀取出压缩包里的若干部件。
func zipParts(r io.ReaderAt, size int64, match func(name string) bool) ([]*zip.File, *zip.Reader, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, nil, fmt.Errorf("文件不是有效的 Office 文档: %w", err)
	}
	var out []*zip.File
	for _, f := range zr.File {
		if match(f.Name) {
			out = append(out, f)
		}
	}
	return out, zr, nil
}

func readPart(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, MaxOutput*4))
}

// docx 抽 Word 正文。
//
// 只解析 w:t（文字）与 w:p（段落）两种标记，忽略样式、修订、批注等一切其它内容——
// 知识库要的是"说了什么"，不是"长什么样"。
func docx(r io.ReaderAt, size int64) (string, error) {
	parts, _, err := zipParts(r, size, func(n string) bool {
		return n == "word/document.xml" ||
			strings.HasPrefix(n, "word/header") || strings.HasPrefix(n, "word/footer")
	})
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", ErrNoText
	}
	var sb strings.Builder
	for _, f := range parts {
		data, err := readPart(f)
		if err != nil {
			continue
		}
		sb.WriteString(ooxmlText(data, "t", map[string]string{"p": "\n", "br": "\n", "tab": "\t"}))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// xlsx 抽 Excel 的单元格文字。
//
// 表格里绝大多数字符串都躺在 sharedStrings.xml 这张共享表里，
// 工作表中只存索引。所以要先把共享表读出来，再按 t="s" 的单元格去查。
func xlsx(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("文件不是有效的 Excel 文档: %w", err)
	}

	var shared []string
	for _, f := range zr.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		data, err := readPart(f)
		if err != nil {
			break
		}
		shared = sharedStrings(data)
		break
	}

	var sb strings.Builder
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		data, err := readPart(f)
		if err != nil {
			continue
		}
		sb.WriteString(sheetText(data, shared))
	}
	return sb.String(), nil
}

// sharedStrings 解析共享字符串表。
func sharedStrings(data []byte) []string {
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI, cur = true, strings.Builder{}
			case "t":
				inT = true
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
				inSI = false
			case "t":
				inT = false
			}
		}
	}
	return out
}

// sheetText 把一张工作表读成按行分隔、按制表符分列的文本。
func sheetText(data []byte, shared []string) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cellType, value string
	var inV, inIS bool
	cells := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				cellType, value = "", ""
				for _, a := range t.Attr {
					if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v":
				inV = true
			case "is": // 内联字符串
				inIS = true
			}
		case xml.CharData:
			if inV || inIS {
				value += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "is":
				inIS = false
			case "c":
				text := value
				if cellType == "s" {
					// t="s" 时 v 里是共享表下标而不是文字本身。
					if idx, ok := atoi(value); ok && idx >= 0 && idx < len(shared) {
						text = shared[idx]
					} else {
						text = ""
					}
				}
				if strings.TrimSpace(text) != "" {
					if cells > 0 {
						sb.WriteString("\t")
					}
					sb.WriteString(text)
					cells++
				}
			case "row":
				if cells > 0 {
					sb.WriteString("\n")
				}
				cells = 0
			}
		}
	}
	return sb.String()
}

// pptx 抽幻灯片上的文字，包含备注页。
func pptx(r io.ReaderAt, size int64) (string, error) {
	parts, _, err := zipParts(r, size, func(n string) bool {
		return (strings.HasPrefix(n, "ppt/slides/slide") ||
			strings.HasPrefix(n, "ppt/notesSlides/notesSlide")) &&
			strings.HasSuffix(n, ".xml")
	})
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", ErrNoText
	}
	var sb strings.Builder
	for _, f := range parts {
		data, err := readPart(f)
		if err != nil {
			continue
		}
		sb.WriteString(ooxmlText(data, "t", map[string]string{"p": "\n", "br": "\n"}))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ooxmlText 通用的 OOXML 取词：收集 textTag 里的字符，遇到 breaks 里的标记就插入分隔符。
func ooxmlText(data []byte, textTag string, breaks map[string]string) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == textTag {
				depth++
			}
		case xml.CharData:
			if depth > 0 {
				sb.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == textTag && depth > 0 {
				depth--
			}
			if sep, ok := breaks[t.Name.Local]; ok {
				sb.WriteString(sep)
			}
		}
	}
	return sb.String()
}

// ===== PDF =====

// pdfText 抽 PDF 的文字层。
//
// 抽不出东西的最常见原因是扫描件——整页是图片，没有文字层，
// 这种情况要靠 OCR，本系统不做。返回 ErrNoText 让上层记成"跳过"并在界面上说明。
func pdfText(r io.ReaderAt, size int64) (string, error) {
	rd, err := pdf.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("PDF 解析失败: %w", err)
	}
	var sb strings.Builder
	pages := rd.NumPage()
	for i := 1; i <= pages; i++ {
		p := rd.Page(i)
		if p.V.IsNull() {
			continue
		}
		// 单页解析失败不该让整份文件失败：一份 300 页的手册里有一页异常字体，
		// 丢掉那一页远好过丢掉整本。
		text, err := safePageText(p)
		if err != nil {
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n")
		if sb.Len() > MaxOutput {
			break
		}
	}
	return sb.String(), nil
}

// safePageText 包一层 recover：ledongthuc/pdf 在遇到畸形字体或加密流时会 panic，
// 不拦住的话一份坏 PDF 能把整个索引任务带崩。
func safePageText(p pdf.Page) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("页面解析异常: %v", r)
		}
	}()
	return p.GetPlainText(nil)
}

// ===== 通用清理 =====

// tidy 压掉多余空白。
//
// 抽出来的文本里常有大段连续空行和行尾空格（尤其是 PDF 和表格），
// 不清掉会白白占掉切块的容量，把真正的内容挤出去。
func tidy(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, " ", " ") // 不换行空格
	s = strings.ReplaceAll(s, string(rune(0xFEFF)), "") // 零宽空格 / BOM

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if strings.TrimSpace(ln) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// truncateRunes 按字符（而非字节）截断，避免把一个汉字劈成两半。
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func atoi(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
