package vector

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []float32{0, 1, -1, 0.123456, -0.987654, 3.4e38, 1e-38}
	out := Decode(Encode(in))
	if len(out) != len(in) {
		t.Fatalf("长度变了: %d -> %d", len(in), len(out))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Fatalf("第 %d 位变了: %v -> %v", i, in[i], out[i])
		}
	}
	if got := Decode([]byte{1, 2, 3}); got != nil {
		t.Fatalf("长度非 4 倍数时应当返回 nil，实际 %v", got)
	}
}

func TestNormalize(t *testing.T) {
	v := []float32{3, 4}
	Normalize(v)
	if math.Abs(float64(v[0]-0.6)) > 1e-6 || math.Abs(float64(v[1]-0.8)) > 1e-6 {
		t.Fatalf("归一化不对: %v", v)
	}
	// 零向量不能除出 NaN——空文本或全零向量真的会出现。
	z := []float32{0, 0, 0}
	Normalize(z)
	for _, f := range z {
		if math.IsNaN(float64(f)) {
			t.Fatal("零向量归一化产生了 NaN")
		}
	}
}

// TestQuantizedSearchMatchesExact 是这个包的核心保证：
// 量化只是为了省内存，不能把该排前面的挤下去。
//
// 造 2000 条随机向量，比较量化扫描与精确计算的排序，
// 要求 Top-1 一致、Top-10 高度重合。
func TestQuantizedSearchMatchesExact(t *testing.T) {
	const (
		n   = 2000
		dim = 256
	)
	rng := rand.New(rand.NewSource(42))

	vecs := make([][]float32, n)
	ix := New(dim, "test")
	for i := range n {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		Normalize(v)
		vecs[i] = v
		cp := make([]float32, dim)
		copy(cp, v)
		ix.Add(uint64(i+1), "hash", cp)
	}

	top1Match, overlap, rounds := 0, 0, 50
	for range rounds {
		q := make([]float32, dim)
		for j := range q {
			q[j] = rng.Float32()*2 - 1
		}
		Normalize(q)

		// 精确结果
		type sc struct {
			id uint64
			s  float32
		}
		exact := make([]sc, n)
		for i := range n {
			exact[i] = sc{uint64(i + 1), Dot(vecs[i], q)}
		}
		sort.Slice(exact, func(a, b int) bool { return exact[a].s > exact[b].s })

		cp := make([]float32, dim)
		copy(cp, q)
		got := ix.Search(cp, 10)
		if len(got) != 10 {
			t.Fatalf("返回条数不对: %d", len(got))
		}
		if got[0].ChunkID == exact[0].id {
			top1Match++
		}
		want := map[uint64]bool{}
		for _, e := range exact[:10] {
			want[e.id] = true
		}
		for _, g := range got {
			if want[g.ChunkID] {
				overlap++
			}
		}
	}

	if top1Match < rounds*9/10 {
		t.Fatalf("Top-1 一致率过低: %d/%d", top1Match, rounds)
	}
	if overlap < rounds*10*9/10 {
		t.Fatalf("Top-10 重合率过低: %d/%d", overlap, rounds*10)
	}
	t.Logf("Top-1 一致 %d/%d，Top-10 重合 %d/%d", top1Match, rounds, overlap, rounds*10)
}

// TestSearchFindsPlantedNeedle 埋一条与查询几乎相同的向量，必须排第一。
func TestSearchFindsPlantedNeedle(t *testing.T) {
	const dim = 128
	rng := rand.New(rand.NewSource(7))
	ix := New(dim, "test")

	needle := make([]float32, dim)
	for j := range needle {
		needle[j] = rng.Float32()
	}
	Normalize(needle)

	for i := range 500 {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		ix.Add(uint64(i+1), "noise", v)
	}
	cp := make([]float32, dim)
	copy(cp, needle)
	ix.Add(9999, "needle", cp)

	q := make([]float32, dim)
	copy(q, needle)
	got := ix.Search(q, 5)
	if len(got) == 0 || got[0].ChunkID != 9999 {
		t.Fatalf("没能把最接近的那条排在第一: %+v", got)
	}
	if got[0].BlobHash != "needle" {
		t.Fatalf("哈希对不上: %q", got[0].BlobHash)
	}
	if got[0].Score < 0.9 {
		t.Fatalf("几乎相同的向量得分却只有 %v", got[0].Score)
	}
}

func TestRemoveBlob(t *testing.T) {
	const dim = 32
	ix := New(dim, "test")
	mk := func() []float32 {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rand.Float32()
		}
		return v
	}
	for i := range 10 {
		ix.Add(uint64(i+1), "keep", mk())
	}
	for i := range 5 {
		ix.Add(uint64(100+i), "drop", mk())
	}
	if ix.Len() != 15 {
		t.Fatalf("收录条数不对: %d", ix.Len())
	}

	if n := ix.RemoveBlob("drop"); n != 5 {
		t.Fatalf("删除条数不对: %d", n)
	}
	if ix.Len() != 10 {
		t.Fatalf("删除后剩余条数不对: %d", ix.Len())
	}
	// 重建后剩下的条目必须仍然对得上自己的 id 与哈希，不能串位。
	for _, h := range ix.Search(mk(), 10) {
		if h.BlobHash != "keep" {
			t.Fatalf("删除后残留了被删内容: %+v", h)
		}
		if h.ChunkID == 0 || h.ChunkID > 10 {
			t.Fatalf("删除后 id 串位了: %+v", h)
		}
	}
	if n := ix.RemoveBlob("nonexistent"); n != 0 {
		t.Fatalf("删不存在的内容应当返回 0，实际 %d", n)
	}
}

func TestSearchGuards(t *testing.T) {
	ix := New(16, "test")
	if got := ix.Search(make([]float32, 16), 5); got != nil {
		t.Fatal("空索引应当返回 nil")
	}
	ix.Add(1, "h", make([]float32, 16))
	if got := ix.Search(make([]float32, 8), 5); got != nil {
		t.Fatal("维度不符的查询应当返回 nil，而不是算出一堆没意义的分数")
	}
	if got := ix.Search(make([]float32, 16), 0); got != nil {
		t.Fatal("topN=0 应当返回 nil")
	}
	// 维度不符的写入要被拒绝，否则整块存储会错位。
	ix.Add(2, "h", make([]float32, 99))
	if ix.Len() != 1 {
		t.Fatalf("维度不符的向量被收进去了: %d", ix.Len())
	}
	// topN 超过总数时返回全部，不能越界。
	if got := ix.Search(make([]float32, 16), 100); len(got) != 1 {
		t.Fatalf("topN 超过总数时应当返回全部，实际 %d", len(got))
	}
}

func TestMemoryEstimateIsSane(t *testing.T) {
	const dim = 1024
	ix := New(dim, "test")
	for i := range 1000 {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rand.Float32()
		}
		ix.Add(uint64(i+1), "h", v)
	}
	got := ix.MemoryBytes()
	// 1000 条 × 1024 维，量化后约 1MB；若退化成 float32 会是 4MB。
	if got < 1_000_000 || got > 1_200_000 {
		t.Fatalf("内存估算偏离预期: %d 字节", got)
	}
	t.Logf("1000 条 1024 维占用 %.2f MB（float32 需 %.2f MB）",
		float64(got)/1e6, float64(1000*dim*4)/1e6)
}
