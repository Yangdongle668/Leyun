package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
	"github.com/Yangdongle668/Leyun/internal/service/extract"
	"github.com/Yangdongle668/Leyun/internal/service/llm"
	"github.com/Yangdongle668/Leyun/internal/service/vector"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// KBConfig 是知识库的运行参数，全部来自管理后台的设置项。
type KBConfig struct {
	Enabled         bool
	BaseURL         string
	APIKey          string
	ChatModel       string
	EmbedModel      string
	EmbedDim        int
	ChunkSize       int
	ChunkOverlap    int
	TopK            int
	SpaceIDs        []uint64
	IncludePersonal bool
	MaxFileSize     int64
}

// Ready 判断配置是否足以开工。
func (c KBConfig) Ready() bool {
	return c.Enabled && c.BaseURL != "" && c.EmbedModel != "" && c.EmbedDim > 0
}

// CanChat 判断是否能回答问题（对话模型可以与向量模型分开配）。
func (c KBConfig) CanChat() bool { return c.Ready() && c.ChatModel != "" }

// 默认值。界面上留空时用这些。
const (
	defaultChunkSize    = 700
	defaultChunkOverlap = 80
	defaultTopK         = 6
	defaultMaxFileSize  = 32 << 20 // 32MB
	// 召回时多取几倍候选再过权限：直接取 TopK 的话，
	// 排在前面的若干条全被权限挡掉，用户就只剩两三条能看的，
	// 明明库里还有他有权看的内容却答不上来。
	candidateMultiplier = 8
	// 单次向量接口最多送多少条文本。多数服务的上限在 16~64 之间，
	// 取 16 兼容性最好。
	embedBatch = 16
	// 连续失败多少次就不再重试这份文档。
	maxIndexAttempts = 3
)

// KBService 是知识库：负责把云盘里的文档变成可检索的向量，
// 并在回答问题时按提问人的权限过滤检索结果。
type KBService struct {
	db      *gorm.DB
	setting *SettingService
	acl     *ACLService
	file    *FileService
	log     *slog.Logger

	mu    sync.RWMutex
	index *vector.Index

	// wake 用来在配置变更或有新文件时立刻叫醒索引协程，
	// 免得管理员配好了却要等下一个轮询周期才看到动静。
	wake    chan struct{}
	stop    chan struct{}
	once    sync.Once
	stopped bool

	// indexing 标记当前是否有一轮索引在跑，供界面展示。
	indexing bool
}

// NewKBService 构造知识库服务。
func NewKBService(db *gorm.DB, setting *SettingService, acl *ACLService, file *FileService, log *slog.Logger) *KBService {
	return &KBService{
		db: db, setting: setting, acl: acl, file: file, log: log,
		wake: make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
}

// Config 从设置表读出当前配置。
func (s *KBService) Config() KBConfig {
	c := KBConfig{
		Enabled:         s.setting.GetBool(store.SettingAIEnabled, false),
		BaseURL:         strings.TrimSpace(s.setting.Get(store.SettingAIBaseURL, "")),
		APIKey:          s.setting.Get(store.SettingAIAPIKey, ""),
		ChatModel:       strings.TrimSpace(s.setting.Get(store.SettingAIChatModel, "")),
		EmbedModel:      strings.TrimSpace(s.setting.Get(store.SettingAIEmbedModel, "")),
		EmbedDim:        int(s.setting.GetInt64(store.SettingAIEmbedDim, 0)),
		ChunkSize:       int(s.setting.GetInt64(store.SettingAIChunkSize, defaultChunkSize)),
		ChunkOverlap:    int(s.setting.GetInt64(store.SettingAIChunkOverlap, defaultChunkOverlap)),
		TopK:            int(s.setting.GetInt64(store.SettingAITopK, defaultTopK)),
		IncludePersonal: s.setting.GetBool(store.SettingAIIncludePersonal, false),
		MaxFileSize:     s.setting.GetInt64(store.SettingAIMaxFileSize, defaultMaxFileSize),
	}
	for _, part := range strings.Split(s.setting.Get(store.SettingAISpaceIDs, ""), ",") {
		if id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64); err == nil && id > 0 {
			c.SpaceIDs = append(c.SpaceIDs, id)
		}
	}
	if c.TopK <= 0 {
		c.TopK = defaultTopK
	}
	if c.MaxFileSize <= 0 {
		c.MaxFileSize = defaultMaxFileSize
	}
	return c
}

// Client 按当前配置构造大模型客户端。
func (s *KBService) Client() *llm.Client {
	c := s.Config()
	return llm.New(llm.Config{
		BaseURL: c.BaseURL, APIKey: c.APIKey,
		ChatModel: c.ChatModel, EmbedModel: c.EmbedModel,
	})
}

// ===== 索引协程 =====

// Start 启动后台索引协程。
func (s *KBService) Start() {
	go s.loop()
	s.Wake()
}

// Stop 停止后台索引协程。
func (s *KBService) Stop() {
	s.once.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		close(s.stop)
	})
}

// Wake 立刻触发一轮索引。channel 带缓冲且非阻塞投递，
// 连续调用不会堆积，也不会卡住调用方。
func (s *KBService) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *KBService) loop() {
	// 轮询是兜底：正常情况下上传、删除都会主动 Wake，
	// 但配置在别处被改、或某次 Wake 恰好撞上正在跑的一轮时，靠它补上。
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		case <-s.wake:
		}
		if err := s.runOnce(); err != nil {
			s.log.Warn("知识库索引出错", "err", err)
		}
	}
}

// runOnce 跑一轮：登记新文档 → 逐个索引 → 清理已删除的。
func (s *KBService) runOnce() error {
	cfg := s.Config()
	if !cfg.Ready() {
		return nil
	}
	s.setIndexing(true)
	defer s.setIndexing(false)

	if err := s.ensureIndexLoaded(cfg); err != nil {
		return err
	}
	if err := s.enqueueDocs(cfg); err != nil {
		return err
	}
	if err := s.purgeOrphans(); err != nil {
		return err
	}
	return s.drainQueue(cfg)
}

func (s *KBService) setIndexing(v bool) {
	s.mu.Lock()
	s.indexing = v
	s.mu.Unlock()
}

// ensureIndexLoaded 首次使用或换了模型时，把数据库里的向量装载进内存。
func (s *KBService) ensureIndexLoaded(cfg KBConfig) error {
	s.mu.RLock()
	ix := s.index
	s.mu.RUnlock()
	if ix != nil && ix.Dim() == cfg.EmbedDim && ix.Model() == cfg.EmbedModel {
		return nil
	}

	fresh := vector.New(cfg.EmbedDim, cfg.EmbedModel)
	const page = 500
	var lastID uint64
	loaded := 0
	for {
		var rows []model.KBChunk
		err := s.db.Where("id > ? AND dim = ?", lastID, cfg.EmbedDim).
			Order("id asc").Limit(page).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("装载向量失败: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			lastID = r.ID
			v := vector.Decode(r.Vector)
			if len(v) != cfg.EmbedDim {
				continue
			}
			fresh.Add(r.ID, r.BlobHash, v)
			loaded++
		}
		if len(rows) < page {
			break
		}
	}

	s.mu.Lock()
	s.index = fresh
	s.mu.Unlock()
	s.log.Info("知识库向量已装载", "片段数", loaded, "模型", cfg.EmbedModel, "维度", cfg.EmbedDim)
	return nil
}

// indexableSpaceIDs 返回参与索引的空间。
//
// 个人空间默认排除：那是员工的私人区域，检索时虽然会被权限过滤挡住，
// 但没必要让它多一份脱离 ACL 的向量副本。
func (s *KBService) indexableSpaceIDs(cfg KBConfig) ([]uint64, error) {
	var spaces []model.Space
	q := s.db.Model(&model.Space{}).Where("enabled = ?", true)
	if len(cfg.SpaceIDs) > 0 {
		q = q.Where("id IN ?", cfg.SpaceIDs)
	} else if !cfg.IncludePersonal {
		q = q.Where("type <> ?", model.SpacePersonal)
	}
	if err := q.Select("id").Find(&spaces).Error; err != nil {
		return nil, fmt.Errorf("查询可索引空间失败: %w", err)
	}
	out := make([]uint64, 0, len(spaces))
	for _, sp := range spaces {
		out = append(out, sp.ID)
	}
	return out, nil
}

// enqueueDocs 扫描节点，把还没建档的内容登记成待索引。
//
// 按 blob_hash 建档而不是 node_id：同一份合同被三个部门各存一份时，
// 抽取和向量化只做一次。
func (s *KBService) enqueueDocs(cfg KBConfig) error {
	spaceIDs, err := s.indexableSpaceIDs(cfg)
	if err != nil {
		return err
	}
	if len(spaceIDs) == 0 {
		return nil
	}

	const page = 500
	var lastID uint64
	for {
		var nodes []model.Node
		err := s.db.Where("id > ? AND is_dir = ? AND trashed = ? AND blob_hash <> '' AND space_id IN ? AND size <= ?",
			lastID, false, false, spaceIDs, cfg.MaxFileSize).
			Order("id asc").Limit(page).Find(&nodes).Error
		if err != nil {
			return fmt.Errorf("扫描待索引文件失败: %w", err)
		}
		if len(nodes) == 0 {
			return nil
		}

		seen := make(map[string]model.Node, len(nodes))
		hashes := make([]string, 0, len(nodes))
		for _, n := range nodes {
			lastID = n.ID
			if !extract.Supported(n.Ext) {
				continue
			}
			if _, ok := seen[n.BlobHash]; !ok {
				seen[n.BlobHash] = n
				hashes = append(hashes, n.BlobHash)
			}
		}
		if len(hashes) > 0 {
			var existing []model.KBDoc
			if err := s.db.Select("blob_hash").Where("blob_hash IN ?", hashes).Find(&existing).Error; err != nil {
				return fmt.Errorf("查询已建档内容失败: %w", err)
			}
			for _, e := range existing {
				delete(seen, e.BlobHash)
			}
			docs := make([]model.KBDoc, 0, len(seen))
			for hash, n := range seen {
				docs = append(docs, model.KBDoc{
					BlobHash: hash, Name: n.Name, Ext: n.Ext,
					Size: n.Size, Status: model.KBPending,
				})
			}
			if len(docs) > 0 {
				// 并发下可能与另一轮撞车，忽略重复即可。
				if err := s.db.Create(&docs).Error; err != nil {
					s.log.Debug("登记待索引内容时有冲突", "err", err)
				}
			}
		}
		if len(nodes) < page {
			return nil
		}
	}
}

// purgeOrphans 清掉已经没有任何节点引用的内容。
//
// 文件被彻底删除后，它的向量必须跟着消失，否则检索还会命中，
// 内容照样进模型上下文——权限过滤那一步救不了它，因为节点都不存在了。
func (s *KBService) purgeOrphans() error {
	var orphans []model.KBDoc
	err := s.db.Where("blob_hash NOT IN (?)",
		s.db.Model(&model.Node{}).Select("blob_hash").Where("blob_hash <> '' AND is_dir = ?", false),
	).Limit(200).Find(&orphans).Error
	if err != nil {
		return fmt.Errorf("查询孤立内容失败: %w", err)
	}
	for _, doc := range orphans {
		if err := s.dropDoc(doc.BlobHash); err != nil {
			return err
		}
	}
	if len(orphans) > 0 {
		s.log.Info("已清理不再被引用的知识库内容", "数量", len(orphans))
	}
	return nil
}

func (s *KBService) dropDoc(blobHash string) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("blob_hash = ?", blobHash).Delete(&model.KBChunk{}).Error; err != nil {
			return err
		}
		return tx.Where("blob_hash = ?", blobHash).Delete(&model.KBDoc{}).Error
	})
	if err != nil {
		return fmt.Errorf("清理知识库内容失败: %w", err)
	}
	s.mu.RLock()
	ix := s.index
	s.mu.RUnlock()
	if ix != nil {
		ix.RemoveBlob(blobHash)
	}
	return nil
}

// drainQueue 处理待索引队列。
func (s *KBService) drainQueue(cfg KBConfig) error {
	client := s.Client()
	for {
		select {
		case <-s.stop:
			return nil
		default:
		}

		var doc model.KBDoc
		err := s.db.Where("status = ? AND attempts < ?", model.KBPending, maxIndexAttempts).
			Order("id asc").First(&doc).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("取待索引内容失败: %w", err)
		}
		if err := s.indexOne(&doc, cfg, client); err != nil {
			s.log.Warn("索引文档失败", "文件", doc.Name, "err", err)
		}
	}
}

// indexOne 抽取、切块、向量化一份内容。
func (s *KBService) indexOne(doc *model.KBDoc, cfg KBConfig, client *llm.Client) error {
	s.db.Model(doc).Updates(map[string]any{
		"status": model.KBIndexing, "attempts": doc.Attempts + 1,
	})

	fail := func(status, msg string) error {
		s.db.Model(doc).Updates(map[string]any{"status": status, "err": clampRunes(msg, 500)})
		return nil
	}

	text, err := s.extractBlob(doc, cfg)
	switch {
	case errors.Is(err, extract.ErrUnsupported):
		return fail(model.KBSkipped, "该类型不提取文字")
	case errors.Is(err, extract.ErrNoText):
		// 扫描件是最常见的来源。记成"跳过"并写清原因，
		// 管理员才知道那份合同其实没进知识库，而不是以为索引好了却检索不到。
		return fail(model.KBSkipped, extract.ErrNoText.Error())
	case err != nil:
		return fail(model.KBFailed, err.Error())
	}

	chunks := extract.Split(text, cfg.ChunkSize, cfg.ChunkOverlap)
	if len(chunks) == 0 {
		return fail(model.KBSkipped, extract.ErrNoText.Error())
	}

	// 重建前先清掉旧片段，避免换模型或重试时留下两份。
	if err := s.db.Where("blob_hash = ?", doc.BlobHash).Delete(&model.KBChunk{}).Error; err != nil {
		return fail(model.KBFailed, "清理旧片段失败："+err.Error())
	}
	s.mu.RLock()
	ix := s.index
	s.mu.RUnlock()
	if ix != nil {
		ix.RemoveBlob(doc.BlobHash)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	stored := 0
	for start := 0; start < len(chunks); start += embedBatch {
		end := min(start+embedBatch, len(chunks))
		batch := chunks[start:end]

		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Text
		}
		vecs, err := client.Embed(ctx, texts)
		if err != nil {
			return fail(model.KBFailed, err.Error())
		}

		rows := make([]model.KBChunk, 0, len(batch))
		for i, c := range batch {
			v := vecs[i]
			if len(v) != cfg.EmbedDim {
				return fail(model.KBFailed, fmt.Sprintf(
					"向量维度不符：配置写的是 %d，模型实际返回 %d", cfg.EmbedDim, len(v)))
			}
			vector.Normalize(v)
			rows = append(rows, model.KBChunk{
				BlobHash: doc.BlobHash, Seq: c.Seq, Text: c.Text,
				Vector: vector.Encode(v), Dim: cfg.EmbedDim,
			})
		}
		if err := s.db.Create(&rows).Error; err != nil {
			return fail(model.KBFailed, "保存片段失败："+err.Error())
		}
		if ix != nil {
			for i, r := range rows {
				ix.Add(r.ID, doc.BlobHash, vecs[i])
			}
		}
		stored += len(rows)
	}

	now := time.Now()
	s.db.Model(doc).Updates(map[string]any{
		"status": model.KBDone, "chunks": stored, "chars": len([]rune(text)),
		"model": cfg.EmbedModel, "dim": cfg.EmbedDim, "err": "", "indexed_at": &now,
	})
	return nil
}

// extractBlob 打开内容并抽取文字。
func (s *KBService) extractBlob(doc *model.KBDoc, cfg KBConfig) (string, error) {
	var node model.Node
	err := s.db.Where("blob_hash = ? AND is_dir = ?", doc.BlobHash, false).First(&node).Error
	if err != nil {
		return "", fmt.Errorf("找不到引用该内容的文件: %w", err)
	}
	if node.Size > cfg.MaxFileSize {
		return "", extract.ErrUnsupported
	}
	f, err := s.file.OpenContent(&node)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return extract.Extract(f, node.Size, doc.Ext)
}

// ===== 检索 =====

// Passage 是一条通过了权限校验的检索结果。
type Passage struct {
	ChunkID uint64  `json:"chunk_id"`
	Text    string  `json:"text"`
	Score   float32 `json:"score"`
	// NodeID 是"这个提问人有权看到的那个引用"。同一份内容可能被多个部门各存一份，
	// 这里给出的一定是他够得着的那一个。
	NodeID    uint64   `json:"node_id"`
	SpaceID   uint64   `json:"space_id"`
	Name      string   `json:"name"`
	PathNames []string `json:"path_names"`
}

// Retrieve 检索并按提问人的权限过滤。
//
// 这是整套知识库的命门：顺序只能是"检索 → 过滤 → 拼进上下文"。
// 反过来做（先把全部命中塞进 prompt，再在系统提示词里叮嘱模型"没权限的别说"）
// 是拦不住的——内容已经进了上下文，模型的顺从性不是访问控制。
// 所以这一步写在代码里，而不是写在文档里靠人自觉。
func (s *KBService) Retrieve(subj *Subject, query string, topK int) ([]Passage, error) {
	cfg := s.Config()
	if !cfg.Ready() {
		return nil, response.BadRequest("知识库尚未配置完成")
	}
	if subj == nil || subj.User == nil {
		return nil, response.Unauthorized("需要登录")
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = cfg.TopK
	}

	if err := s.ensureIndexLoaded(cfg); err != nil {
		return nil, err
	}
	s.mu.RLock()
	ix := s.index
	s.mu.RUnlock()
	if ix == nil || ix.Len() == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vecs, err := s.Client().Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("向量化提问失败: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) != cfg.EmbedDim {
		return nil, fmt.Errorf("提问的向量维度与索引不符，可能是换了模型没有重建索引")
	}

	// 多取候选：直接取 TopK 的话，排前面的若干条一旦全被权限挡掉，
	// 用户就只剩两三条能看的，明明库里还有他有权看的内容却答不上来。
	hits := ix.Search(vecs[0], topK*candidateMultiplier)
	if len(hits) == 0 {
		return nil, nil
	}
	hits = s.rerankExact(hits, vecs[0])

	return s.filterByPermission(subj, hits, topK)
}

// rerankExact 用数据库里的原始 float32 向量重算一遍相似度。
//
// 内存索引是 int8 量化的，省了四倍内存，代价是千分之几的误差。
// 候选集只有几十条，重算的代价可以忽略，但能把量化带来的次序抖动抹平。
func (s *KBService) rerankExact(hits []vector.Hit, query []float32) []vector.Hit {
	ids := make([]uint64, len(hits))
	for i, h := range hits {
		ids[i] = h.ChunkID
	}
	var rows []model.KBChunk
	if err := s.db.Select("id", "vector").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return hits // 精排失败不致命，用量化分数继续
	}
	exact := make(map[uint64]float32, len(rows))
	for _, r := range rows {
		if v := vector.Decode(r.Vector); len(v) == len(query) {
			exact[r.ID] = vector.Dot(v, query)
		}
	}
	for i := range hits {
		if s, ok := exact[hits[i].ChunkID]; ok {
			hits[i].Score = s
		}
	}
	sortHitsDesc(hits)
	return hits
}

func sortHitsDesc(hits []vector.Hit) {
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
}

// filterByPermission 把提问人看不到的片段剔掉。
func (s *KBService) filterByPermission(subj *Subject, hits []vector.Hit, topK int) ([]Passage, error) {
	hashes := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for _, h := range hits {
		if !seen[h.BlobHash] {
			seen[h.BlobHash] = true
			hashes = append(hashes, h.BlobHash)
		}
	}

	// 一份内容可能被多个部门各存一份，逐个哈希查出全部引用它的节点。
	var nodes []model.Node
	err := s.db.Where("blob_hash IN ? AND is_dir = ? AND trashed = ?", hashes, false, false).
		Find(&nodes).Error
	if err != nil {
		return nil, fmt.Errorf("查询引用节点失败: %w", err)
	}
	if len(nodes) == 0 {
		return nil, nil
	}

	perms, err := s.acl.EffectiveBatch(subj, nodes)
	if err != nil {
		return nil, err
	}
	// 每份内容留一个"这个人看得见"的引用。看不见的内容连同文件名一起丢弃——
	// 文件名本身经常就是敏感信息（"2026年裁员名单.xlsx"）。
	visible := make(map[string]*model.Node, len(hashes))
	for i := range nodes {
		n := &nodes[i]
		if !perms[n.ID].Has(model.PermView) {
			continue
		}
		if _, ok := visible[n.BlobHash]; !ok {
			visible[n.BlobHash] = n
		}
	}
	if len(visible) == 0 {
		return nil, nil
	}

	// 取正文。到这一步才读 text，前面被挡掉的内容根本不会离开数据库。
	keep := make([]vector.Hit, 0, topK)
	for _, h := range hits {
		if visible[h.BlobHash] == nil {
			continue
		}
		keep = append(keep, h)
		if len(keep) >= topK {
			break
		}
	}
	if len(keep) == 0 {
		return nil, nil
	}
	ids := make([]uint64, len(keep))
	for i, h := range keep {
		ids[i] = h.ChunkID
	}
	var chunks []model.KBChunk
	if err := s.db.Select("id", "blob_hash", "text").Where("id IN ?", ids).Find(&chunks).Error; err != nil {
		return nil, fmt.Errorf("读取片段正文失败: %w", err)
	}
	texts := make(map[uint64]string, len(chunks))
	for _, c := range chunks {
		texts[c.ID] = c.Text
	}

	pathNames, err := s.pathNamesFor(nodes)
	if err != nil {
		return nil, err
	}

	out := make([]Passage, 0, len(keep))
	for _, h := range keep {
		n := visible[h.BlobHash]
		out = append(out, Passage{
			ChunkID: h.ChunkID, Text: texts[h.ChunkID], Score: h.Score,
			NodeID: n.ID, SpaceID: n.SpaceID, Name: n.Name,
			PathNames: pathNames[n.ID],
		})
	}
	return out, nil
}

// pathNamesFor 把物化路径上的 ID 翻成目录名，让引用能显示成人看得懂的位置。
func (s *KBService) pathNamesFor(nodes []model.Node) (map[uint64][]string, error) {
	idSet := map[uint64]struct{}{}
	for _, n := range nodes {
		for _, id := range treex.IDs(n.Path) {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return map[uint64][]string{}, nil
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var named []model.Node
	if err := s.db.Select("id", "name").Where("id IN ?", ids).Find(&named).Error; err != nil {
		return nil, fmt.Errorf("查询路径名称失败: %w", err)
	}
	byID := make(map[uint64]string, len(named))
	for _, n := range named {
		byID[n.ID] = n.Name
	}
	out := make(map[uint64][]string, len(nodes))
	for _, n := range nodes {
		var parts []string
		for _, id := range treex.IDs(n.Path) {
			if name, ok := byID[id]; ok {
				parts = append(parts, name)
			}
		}
		out[n.ID] = parts
	}
	return out, nil
}

// ===== 状态 =====

// KBStatus 是给界面看的索引概况。
type KBStatus struct {
	Enabled    bool   `json:"enabled"`
	Ready      bool   `json:"ready"`
	CanChat    bool   `json:"can_chat"`
	Indexing   bool   `json:"indexing"`
	Total      int64  `json:"total"`
	Done       int64  `json:"done"`
	Pending    int64  `json:"pending"`
	Skipped    int64  `json:"skipped"`
	Failed     int64  `json:"failed"`
	Chunks     int64  `json:"chunks"`
	IndexedMem int64  `json:"indexed_mem"`
	EmbedModel string `json:"embed_model"`
	ChatModel  string `json:"chat_model"`
	Dim        int    `json:"dim"`
}

// Status 汇总当前索引情况。
func (s *KBService) Status() (*KBStatus, error) {
	cfg := s.Config()
	st := &KBStatus{
		Enabled: cfg.Enabled, Ready: cfg.Ready(), CanChat: cfg.CanChat(),
		EmbedModel: cfg.EmbedModel, ChatModel: cfg.ChatModel, Dim: cfg.EmbedDim,
	}
	s.mu.RLock()
	st.Indexing = s.indexing
	ix := s.index
	s.mu.RUnlock()
	if ix != nil {
		st.IndexedMem = ix.MemoryBytes()
	}

	type row struct {
		Status string
		N      int64
	}
	var rows []row
	if err := s.db.Model(&model.KBDoc{}).Select("status, count(*) as n").Group("status").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("统计索引状态失败: %w", err)
	}
	for _, r := range rows {
		st.Total += r.N
		switch r.Status {
		case model.KBDone:
			st.Done = r.N
		case model.KBPending, model.KBIndexing:
			st.Pending += r.N
		case model.KBSkipped:
			st.Skipped = r.N
		case model.KBFailed:
			st.Failed = r.N
		}
	}
	if err := s.db.Model(&model.KBChunk{}).Count(&st.Chunks).Error; err != nil {
		return nil, fmt.Errorf("统计片段数失败: %w", err)
	}
	return st, nil
}

// Reindex 清空并重建全部索引。换了向量模型后必须做一次。
func (s *KBService) Reindex() error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&model.KBChunk{}).Error; err != nil {
			return err
		}
		return tx.Where("1 = 1").Delete(&model.KBDoc{}).Error
	})
	if err != nil {
		return fmt.Errorf("清空知识库索引失败: %w", err)
	}
	cfg := s.Config()
	s.mu.Lock()
	s.index = vector.New(cfg.EmbedDim, cfg.EmbedModel)
	s.mu.Unlock()
	s.Wake()
	return nil
}

// RetryFailed 把失败的文档放回队列。
func (s *KBService) RetryFailed() (int64, error) {
	res := s.db.Model(&model.KBDoc{}).Where("status = ?", model.KBFailed).
		Updates(map[string]any{"status": model.KBPending, "attempts": 0, "err": ""})
	if res.Error != nil {
		return 0, fmt.Errorf("重置失败文档失败: %w", res.Error)
	}
	s.Wake()
	return res.RowsAffected, nil
}

// FailedDocs 列出索引失败与被跳过的文档，供管理员排查。
func (s *KBService) FailedDocs(limit int) ([]model.KBDoc, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var docs []model.KBDoc
	err := s.db.Where("status IN ?", []string{model.KBFailed, model.KBSkipped}).
		Order("updated_at desc").Limit(limit).Find(&docs).Error
	if err != nil {
		return nil, fmt.Errorf("查询失败文档失败: %w", err)
	}
	return docs, nil
}

func clampRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
