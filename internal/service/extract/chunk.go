package extract

import "strings"

// Chunk 是切好的一个文本块。
type Chunk struct {
	Seq  int
	Text string
}

// 切块参数的合理区间。超出范围的配置一律夹回来，
// 免得管理员在界面上填个 0 或者 999999 把索引搞瘫。
const (
	MinChunkSize = 120
	MaxChunkSize = 4000
)

// Split 把长文本切成适合做向量检索的块。
//
// 两个关键点：
//
//  1. 一律按字符（rune）计数，不按字节，也不按空格分词。
//     中文没有空格分词，按字节切还会把一个汉字劈成两半。
//  2. 优先在段落、其次在句子边界切。硬切会把"甲方应当在三十日内"
//     和"支付全部款项"分到两块里，检索到前半句的人得到的是个残句。
//
// overlap 让相邻块首尾重叠一小段，避免答案正好骑在边界上时两块都只有一半。
func Split(text string, size, overlap int) []Chunk {
	if size < MinChunkSize {
		size = MinChunkSize
	}
	if size > MaxChunkSize {
		size = MaxChunkSize
	}
	// 重叠不能超过块长的一半，否则块与块之间大面积重复，
	// 既浪费向量额度，又让检索结果里挤满几乎一样的片段。
	if overlap < 0 {
		overlap = 0
	}
	if overlap > size/2 {
		overlap = size / 2
	}

	paras := splitParagraphs(text)
	if len(paras) == 0 {
		return nil
	}

	var out []Chunk
	var cur []rune
	flush := func() {
		if len(strings.TrimSpace(string(cur))) == 0 {
			cur = nil
			return
		}
		out = append(out, Chunk{Seq: len(out), Text: strings.TrimSpace(string(cur))})
		if overlap > 0 && len(cur) > overlap {
			// 留一段尾巴作为下一块的开头。
			tail := make([]rune, overlap)
			copy(tail, cur[len(cur)-overlap:])
			cur = tail
		} else {
			cur = nil
		}
	}

	// 每块要留出 overlap 个字符给上一块带过来的尾巴，
	// 否则"满负荷的一块 + 结转的尾巴"会直接超出 size。
	budget := size - overlap
	if budget < MinChunkSize/2 {
		budget = size
	}

	add := func(piece []rune) {
		if len(cur)+len(piece)+1 > size {
			flush()
		}
		// flush 之后 cur 里可能还留着结转的尾巴，加上这一段仍可能超长。
		// 这时宁可丢掉重叠也不能超出上限——超长块会被向量接口直接拒绝。
		if len(cur)+len(piece)+1 > size {
			cur = nil
		}
		cur = appendPara(cur, piece)
	}

	for _, p := range paras {
		r := []rune(p)
		// 单段就超长（长表格、没有分段的合同正文）：先按句子拆开再说。
		if len(r) > budget {
			for _, piece := range splitLongParagraph(r, budget) {
				add(piece)
			}
			continue
		}
		add(r)
	}
	flush()

	// flush 里的重叠逻辑可能在最后留下一段与前一块完全重复的尾巴，去掉它。
	if n := len(out); n >= 2 && strings.HasSuffix(out[n-2].Text, out[n-1].Text) {
		out = out[:n-1]
	}
	return out
}

func appendPara(cur, r []rune) []rune {
	if len(cur) > 0 {
		cur = append(cur, '\n')
	}
	return append(cur, r...)
}

// splitParagraphs 按空行与换行拆段，丢掉纯空白段。
func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// 句末标点。中英文都列上——企业文档里两种混排非常常见。
var sentenceEnd = map[rune]bool{
	'。': true, '！': true, '？': true, '；': true, '…': true,
	'.': true, '!': true, '?': true, ';': true,
	'\t': true, '，': true, ',': true,
}

// splitLongParagraph 把超长的一段按句子边界拆成若干不超过 size 的片段。
//
// 找不到句子边界时（比如一整行没有标点的表格数据）才硬切，
// 这时至少保证切在字符边界上，不会产生半个汉字。
func splitLongParagraph(r []rune, size int) [][]rune {
	var out [][]rune
	for len(r) > size {
		cut := -1
		// 从 size 处往回找最近的句末标点，但不要退得太多——
		// 退过头会切出一堆很短的碎块，反而降低检索质量。
		for i := size - 1; i >= size/2; i-- {
			if sentenceEnd[r[i]] {
				cut = i + 1
				break
			}
		}
		if cut <= 0 {
			cut = size
		}
		piece := make([]rune, cut)
		copy(piece, r[:cut])
		out = append(out, piece)
		r = r[cut:]
	}
	if len(r) > 0 {
		out = append(out, r)
	}
	return out
}
