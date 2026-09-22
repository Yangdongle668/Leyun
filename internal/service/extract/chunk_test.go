package extract

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitKeepsParagraphsWhole(t *testing.T) {
	text := strings.Join([]string{
		"第一章 部门与权限",
		"部门树是权限的骨架，目录权限沿组织架构继承。",
		"第二章 授权对象",
		"授权可以指定到人、指定到部门、按角色，或者面向全体成员。",
	}, "\n")

	chunks := Split(text, 200, 0)
	if len(chunks) != 1 {
		t.Fatalf("这点内容不该被切开，实际切成 %d 块", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "面向全体成员") {
		t.Fatalf("内容丢了: %q", chunks[0].Text)
	}
}

// TestSplitNeverBreaksRunes 切块一律按字符算，绝不能切出半个汉字。
func TestSplitNeverBreaksRunes(t *testing.T) {
	text := strings.Repeat("合同条款约定甲方应当在三十日内支付全部款项。", 60)
	for _, size := range []int{120, 200, 333, 1000} {
		for _, chunk := range Split(text, size, 20) {
			if !utf8.ValidString(chunk.Text) {
				t.Fatalf("size=%d 切出了非法 UTF-8: %q", size, chunk.Text)
			}
			if n := len([]rune(chunk.Text)); n > size {
				t.Fatalf("size=%d 切出了超长块：%d 字符", size, n)
			}
		}
	}
}

// TestSplitCoversEverything 切块不能丢内容。
//
// 把所有块拼起来（去掉重叠），原文的每个句子都应当还在。
func TestSplitCoversEverything(t *testing.T) {
	var lines []string
	for i := range 40 {
		lines = append(lines, "第"+string(rune('A'+i%26))+"条：这里是一段用于覆盖率检查的合同条款正文。")
	}
	text := strings.Join(lines, "\n")

	joined := ""
	for _, c := range Split(text, 150, 30) {
		joined += c.Text
	}
	for _, ln := range lines {
		if !strings.Contains(joined, ln) {
			t.Fatalf("切块后丢了这一行: %q", ln)
		}
	}
}

// TestSplitOverlaps 相邻块要有重叠，避免答案正好骑在边界上时两边都只剩一半。
func TestSplitOverlaps(t *testing.T) {
	text := strings.Repeat("甲方应当在三十日内支付全部款项并提供正式发票。", 40)
	chunks := Split(text, 200, 40)
	if len(chunks) < 3 {
		t.Fatalf("样本应当被切成多块，实际 %d 块", len(chunks))
	}
	overlapped := 0
	for i := 1; i < len(chunks); i++ {
		prev := []rune(chunks[i-1].Text)
		tail := string(prev[max(0, len(prev)-40):])
		// 重叠段落经过 TrimSpace，取一小截比对即可
		probe := []rune(tail)
		if len(probe) > 10 && strings.Contains(chunks[i].Text, string(probe[:10])) {
			overlapped++
		}
	}
	if overlapped == 0 {
		t.Fatal("相邻块之间完全没有重叠")
	}
}

// TestSplitLongUnpunctuatedLine 没有标点的长行（表格数据、日志）也要能切开，
// 而不是硬塞成一个超长块。
func TestSplitLongUnpunctuatedLine(t *testing.T) {
	text := strings.Repeat("研发中心市场部财务部人力资源部", 200) // 一行到底，无标点
	chunks := Split(text, 200, 0)
	if len(chunks) < 5 {
		t.Fatalf("超长无标点行没有被切开，实际 %d 块", len(chunks))
	}
	for _, c := range chunks {
		if n := len([]rune(c.Text)); n > 200 {
			t.Fatalf("切出了超长块：%d 字符", n)
		}
	}
}

// TestSplitClampsInsaneConfig 管理员在界面上填了荒唐的参数也不能把索引搞瘫。
func TestSplitClampsInsaneConfig(t *testing.T) {
	text := strings.Repeat("一段正常的中文内容。", 100)
	for _, tc := range []struct{ size, overlap int }{
		{0, 0}, {-5, -5}, {10, 999}, {999999, 999999}, {200, 200},
	} {
		chunks := Split(text, tc.size, tc.overlap)
		if len(chunks) == 0 {
			t.Fatalf("size=%d overlap=%d 竟然什么都没切出来", tc.size, tc.overlap)
		}
		for _, c := range chunks {
			if strings.TrimSpace(c.Text) == "" {
				t.Fatalf("size=%d overlap=%d 切出了空块", tc.size, tc.overlap)
			}
			if n := len([]rune(c.Text)); n > MaxChunkSize {
				t.Fatalf("size=%d 切出了 %d 字符的块，超过上限", tc.size, n)
			}
		}
	}
}

func TestSplitEmpty(t *testing.T) {
	for _, s := range []string{"", "   ", "\n\n\n", "\t \n "} {
		if got := Split(s, 200, 20); len(got) != 0 {
			t.Fatalf("空白输入应当切不出块，实际 %d 块: %#v", len(got), got)
		}
	}
}

func TestSplitSeqIsSequential(t *testing.T) {
	text := strings.Repeat("一段用于检查序号的中文内容。", 200)
	chunks := Split(text, 150, 20)
	for i, c := range chunks {
		if c.Seq != i {
			t.Fatalf("第 %d 块的序号是 %d", i, c.Seq)
		}
	}
}
