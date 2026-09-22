// Package vector 是知识库的向量存储与检索。
//
// 刻意不引外部向量库（Milvus、Qdrant、pgvector）：乐云的部署承诺是
// "一个可执行文件 + 一个数据库"，为了检索再拉起一套有状态服务，
// 对目标用户（自建、几十到几千人的企业）来说代价远大于收益。
//
// 取而代之的是最朴素的做法：向量存数据库，内存里放一份量化副本做暴力扫描。
// 几十万片段这个量级，暴力扫描的耗时是毫秒级，完全够用。
package vector

import (
	"encoding/binary"
	"math"
	"sort"
	"sync"
)

// Encode 把向量编成 float32 小端序字节，用于落库。
func Encode(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// Decode 把落库的字节解回向量。长度不是 4 的倍数时返回 nil。
func Decode(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// Normalize 就地做 L2 归一化。
//
// 归一化之后余弦相似度就等于点积，检索时每条记录省掉一次开方和除法。
// 几十万条上累积下来不是小数目。
func Normalize(v []float32) {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// Dot 计算点积。两个向量都归一化过时，它就是余弦相似度。
func Dot(a, b []float32) float32 {
	n := min(len(a), len(b))
	var sum float32
	for i := range n {
		sum += a[i] * b[i]
	}
	return sum
}

// Hit 是一条检索结果。
type Hit struct {
	ChunkID  uint64
	BlobHash string
	Score    float32
}

// Index 是内存里的向量索引。
//
// 用 int8 量化而不是直接存 float32：1024 维的话，float32 每条 4KB，
// 20 万条就是 800MB——自建用户的机器未必有这么多内存给一个网盘。
// 量化到 int8 后降到 200MB，代价是相似度有千分之几的误差。
// 所以扫描只用来选出候选，最终排序仍用数据库里的原始向量精算（见 service 层的精排）。
type Index struct {
	mu  sync.RWMutex
	dim int
	// model 记录这份索引是用哪个向量模型建的。换模型后旧向量与新查询
	// 不在同一个语义空间里，算出来的相似度没有意义，必须整体重建。
	model string

	ids   []uint64
	scale []float32
	// q 是所有向量拼成的一整块，第 i 条占 q[i*dim : (i+1)*dim]。
	// 用一整块而不是 [][]int8：20 万个小切片的头部开销和 GC 扫描成本都很可观。
	q []int8
	// hashIdx 指向 hashes 里的下标。片段按内容哈希归属，
	// 而 64 字符的哈希串每条存一份太浪费——同一份文档的几十个片段共用一个。
	hashIdx []int32
	hashes  []string
	hashPos map[string]int32
}

// New 建一个空索引。
func New(dim int, model string) *Index {
	return &Index{dim: dim, model: model, hashPos: map[string]int32{}}
}

// Dim 返回维度。
func (ix *Index) Dim() int { return ix.dim }

// Model 返回建索引时用的向量模型名。
func (ix *Index) Model() string { return ix.model }

// Len 返回已收录的片段数。
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.ids)
}

// MemoryBytes 估算索引占用的内存，用于在管理界面上如实告诉管理员代价。
func (ix *Index) MemoryBytes() int64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return int64(len(ix.q)) + int64(len(ix.ids))*(8+4+4)
}

// Add 收录一条向量。传入的 vec 会被就地归一化。
func (ix *Index) Add(chunkID uint64, blobHash string, vec []float32) {
	if len(vec) != ix.dim {
		return
	}
	Normalize(vec)
	q, scale := quantize(vec)

	ix.mu.Lock()
	defer ix.mu.Unlock()
	pos, ok := ix.hashPos[blobHash]
	if !ok {
		pos = int32(len(ix.hashes))
		ix.hashes = append(ix.hashes, blobHash)
		ix.hashPos[blobHash] = pos
	}
	ix.ids = append(ix.ids, chunkID)
	ix.scale = append(ix.scale, scale)
	ix.q = append(ix.q, q...)
	ix.hashIdx = append(ix.hashIdx, pos)
}

// RemoveBlob 移除某份内容的全部片段。
//
// 用标记加紧凑重建而不是原地挪动：删除在知识库里是低频操作
// （文件被彻底删除时才发生），为它维护空洞表不值得。
func (ix *Index) RemoveBlob(blobHash string) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	pos, ok := ix.hashPos[blobHash]
	if !ok {
		return 0
	}

	keep := make([]int, 0, len(ix.ids))
	for i, h := range ix.hashIdx {
		if h != pos {
			keep = append(keep, i)
		}
	}
	removed := len(ix.ids) - len(keep)
	if removed == 0 {
		return 0
	}

	ids := make([]uint64, 0, len(keep))
	scale := make([]float32, 0, len(keep))
	q := make([]int8, 0, len(keep)*ix.dim)
	hashIdx := make([]int32, 0, len(keep))
	for _, i := range keep {
		ids = append(ids, ix.ids[i])
		scale = append(scale, ix.scale[i])
		q = append(q, ix.q[i*ix.dim:(i+1)*ix.dim]...)
		hashIdx = append(hashIdx, ix.hashIdx[i])
	}
	ix.ids, ix.scale, ix.q, ix.hashIdx = ids, scale, q, hashIdx
	// hashes 里留个空位就行：重建整张表要把所有 hashIdx 重新映射，
	// 而一个空字符串的代价可以忽略。
	ix.hashes[pos] = ""
	delete(ix.hashPos, blobHash)
	return removed
}

// Search 扫描全部向量，返回相似度最高的 topN 条。
//
// query 会被就地归一化。查询向量保持 float32 与量化后的库向量相乘
// （非对称量化）：只量化一边，精度损失比两边都量化小得多，
// 而这一侧只有一条向量，不占内存。
func (ix *Index) Search(query []float32, topN int) []Hit {
	if topN <= 0 {
		return nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if len(query) != ix.dim || len(ix.ids) == 0 {
		return nil
	}
	Normalize(query)

	// 小顶堆会更快，但几十万条的规模下，"全算一遍再部分排序"
	// 的常数更小也更不容易写错。
	scores := make([]Hit, len(ix.ids))
	for i := range ix.ids {
		row := ix.q[i*ix.dim : (i+1)*ix.dim]
		var acc float32
		for j, qv := range row {
			acc += float32(qv) * query[j]
		}
		scores[i] = Hit{
			ChunkID:  ix.ids[i],
			BlobHash: ix.hashes[ix.hashIdx[i]],
			Score:    acc * ix.scale[i],
		}
	}
	if topN > len(scores) {
		topN = len(scores)
	}
	sort.Slice(scores, func(a, b int) bool { return scores[a].Score > scores[b].Score })
	return scores[:topN]
}

// quantize 把归一化后的向量压成 int8，同时给出还原用的比例。
//
// 按每条向量各自的最大绝对值定比例（per-vector scale），而不是全库共用一个：
// 不同文本的向量幅度差异不小，共用比例会让幅度小的那些几乎全被压成 0。
func quantize(v []float32) ([]int8, float32) {
	var maxAbs float32
	for _, f := range v {
		if a := float32(math.Abs(float64(f))); a > maxAbs {
			maxAbs = a
		}
	}
	out := make([]int8, len(v))
	if maxAbs == 0 {
		return out, 0
	}
	scale := maxAbs / 127
	inv := 1 / scale
	for i, f := range v {
		x := float64(f * inv)
		// 四舍五入而不是截断：截断会引入系统性的向零偏移，
		// 在 1024 维上累积起来足以改变排序。
		r := math.Round(x)
		if r > 127 {
			r = 127
		} else if r < -127 {
			r = -127
		}
		out[i] = int8(r)
	}
	return out, scale
}
