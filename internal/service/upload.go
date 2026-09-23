package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
	"github.com/Yangdongle668/Leyun/internal/storage"
)

// UploadService 负责普通上传与分片上传（断点续传 + 秒传）。
type UploadService struct {
	db    *gorm.DB
	cfg   *config.Config
	store *storage.Store
	file  *FileService
	acl   *ACLService
	space *SpaceService
}

// NewUploadService 构造上传服务。
func NewUploadService(db *gorm.DB, cfg *config.Config, store *storage.Store, file *FileService, acl *ACLService, space *SpaceService) *UploadService {
	return &UploadService{db: db, cfg: cfg, store: store, file: file, acl: acl, space: space}
}

// SimpleUpload 直接上传一个小文件。mode 决定撞上同名文件时怎么办。
func (s *UploadService) SimpleUpload(subj *Subject, spaceID, parentID uint64, filename string, size int64, r io.Reader, mode ConflictMode) (*model.Node, error) {
	space, parent, err := s.checkTarget(subj, spaceID, parentID)
	if err != nil {
		return nil, err
	}
	if s.cfg.Storage.MaxUploadSize > 0 && size > s.cfg.Storage.MaxUploadSize {
		return nil, response.QuotaExceeded(fmt.Sprintf("单文件不能超过 %s", HumanSize(s.cfg.Storage.MaxUploadSize)))
	}
	if err := s.space.CheckQuota(space, size); err != nil {
		return nil, err
	}

	hash, written, err := s.store.Put(r)
	if err != nil {
		return nil, err
	}
	if err := s.space.CheckQuota(space, written); err != nil {
		return nil, err
	}

	// 解析重名要在开事务之前：SQLite 只有一条连接，事务里再去查权限会死等。
	plan, err := s.file.PlanConflict(subj, space, parentID, filename, mode)
	if err != nil {
		return nil, err
	}

	var node *model.Node
	err = s.db.Transaction(func(tx *gorm.DB) error {
		n, err := s.file.CreateFile(tx, subj, space, parent, filename, hash, written, plan)
		if err != nil {
			return err
		}
		node = n
		return nil
	})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// checkTarget 校验目标空间与目录，并确认有上传权限。
func (s *UploadService) checkTarget(subj *Subject, spaceID, parentID uint64) (*model.Space, *model.Node, error) {
	space, err := s.space.Get(spaceID)
	if err != nil {
		return nil, nil, err
	}
	parent, err := s.file.GetNode(spaceID, parentID)
	if err != nil {
		return nil, nil, err
	}
	if parent != nil {
		if !parent.IsDir {
			return nil, nil, response.BadRequest("目标不是目录")
		}
		if parent.Trashed {
			return nil, nil, response.BadRequest("目标目录已在回收站中")
		}
	}
	if _, err := s.acl.Require(subj, space, parent, model.PermUpload); err != nil {
		return nil, nil, err
	}
	return space, parent, nil
}

// InitInput 是初始化分片上传的入参。
type InitInput struct {
	SpaceID  uint64 `json:"space_id"`
	ParentID uint64 `json:"parent_id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	// Hash 是整文件 SHA-256，前端能算就传，用于秒传。
	Hash string `json:"hash"`
	// Conflict 是撞上同名文件时的处理方式，取值见 ConflictMode。留空按"保留两者"。
	Conflict string `json:"conflict"`
}

// InitResult 是初始化分片上传的返回。
type InitResult struct {
	// Instant 为 true 表示命中秒传，文件已经建好，无需再传分片。
	Instant    bool        `json:"instant"`
	Node       *model.Node `json:"node,omitempty"`
	UploadID   string      `json:"upload_id,omitempty"`
	ChunkSize  int64       `json:"chunk_size,omitempty"`
	ChunkCount int         `json:"chunk_count,omitempty"`
	// Uploaded 是已经收到的分片序号，前端据此跳过已传部分实现断点续传。
	Uploaded []int `json:"uploaded"`
}

// Init 初始化一次分片上传。命中秒传时直接返回建好的文件。
func (s *UploadService) Init(subj *Subject, in InitInput) (*InitResult, error) {
	space, parent, err := s.checkTarget(subj, in.SpaceID, in.ParentID)
	if err != nil {
		return nil, err
	}
	if in.Size < 0 {
		return nil, response.BadRequest("文件大小不合法")
	}
	if s.cfg.Storage.MaxUploadSize > 0 && in.Size > s.cfg.Storage.MaxUploadSize {
		return nil, response.QuotaExceeded(fmt.Sprintf("单文件不能超过 %s", HumanSize(s.cfg.Storage.MaxUploadSize)))
	}
	if err := s.space.CheckQuota(space, in.Size); err != nil {
		return nil, err
	}
	mode := ParseConflictMode(in.Conflict)

	// 秒传：内容已经在库里就只登记一条元数据。
	if in.Hash != "" {
		blob, err := s.file.FindByHash(in.Hash)
		if err != nil {
			return nil, err
		}
		if blob != nil {
			plan, err := s.file.PlanConflict(subj, space, in.ParentID, in.Filename, mode)
			if err != nil {
				return nil, err
			}
			var node *model.Node
			err = s.db.Transaction(func(tx *gorm.DB) error {
				n, err := s.file.CreateFile(tx, subj, space, parent, in.Filename, blob.Hash, blob.Size, plan)
				if err != nil {
					return err
				}
				node = n
				return nil
			})
			if err != nil {
				return nil, err
			}
			return &InitResult{Instant: true, Node: node, Uploaded: []int{}}, nil
		}
	}

	// 同一个人、同一个目标、同一份内容的未完成会话可以直接续传。
	if in.Hash != "" {
		var existing model.UploadSession
		err := s.db.Where("user_id = ? AND space_id = ? AND parent_id = ? AND hash = ? AND completed = ? AND expire_at > ?",
			subj.User.ID, in.SpaceID, in.ParentID, in.Hash, false, time.Now()).First(&existing).Error
		if err == nil {
			received, err := s.store.ReceivedChunks(existing.UploadID)
			if err != nil {
				return nil, err
			}
			// 续传时用户可能改了主意（上次选"保留两者"，这次选"替换"），以本次为准。
			if existing.Conflict != string(mode) {
				if err := s.db.Model(&model.UploadSession{}).Where("id = ?", existing.ID).
					Update("conflict", string(mode)).Error; err != nil {
					return nil, fmt.Errorf("更新上传会话失败: %w", err)
				}
			}
			return &InitResult{
				UploadID:   existing.UploadID,
				ChunkSize:  existing.ChunkSize,
				ChunkCount: existing.ChunkCount,
				Uploaded:   received,
			}, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("查询上传会话失败: %w", err)
		}
	}

	uploadID, err := hashx.RandomToken(16)
	if err != nil {
		return nil, err
	}
	chunkSize := s.cfg.Storage.ChunkSize
	chunkCount := 1
	if in.Size > 0 {
		chunkCount = int((in.Size + chunkSize - 1) / chunkSize)
	}
	if chunkCount == 0 {
		chunkCount = 1
	}

	session := &model.UploadSession{
		UploadID:   uploadID,
		UserID:     subj.User.ID,
		SpaceID:    in.SpaceID,
		ParentID:   in.ParentID,
		Filename:   sanitizeName(in.Filename),
		Size:       in.Size,
		ChunkSize:  chunkSize,
		ChunkCount: chunkCount,
		Hash:       in.Hash,
		Conflict:   string(mode),
		ExpireAt:   time.Now().Add(s.cfg.Storage.UploadSessionTTL),
	}
	if session.Filename == "" {
		return nil, response.BadRequest("文件名不能为空")
	}
	if err := s.db.Create(session).Error; err != nil {
		return nil, fmt.Errorf("创建上传会话失败: %w", err)
	}
	if err := s.store.PrepareUpload(uploadID); err != nil {
		return nil, err
	}
	return &InitResult{
		UploadID:   uploadID,
		ChunkSize:  chunkSize,
		ChunkCount: chunkCount,
		Uploaded:   []int{},
	}, nil
}

// PutChunk 接收一个分片。
func (s *UploadService) PutChunk(subj *Subject, uploadID string, index int, r io.Reader) ([]int, error) {
	session, err := s.loadSession(subj, uploadID)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= session.ChunkCount {
		return nil, response.BadRequest("分片序号超出范围")
	}
	if _, err := s.store.WriteChunk(uploadID, index, r); err != nil {
		return nil, err
	}
	received, err := s.store.ReceivedChunks(uploadID)
	if err != nil {
		return nil, err
	}
	// 进度同时记在库里，换台设备也能查到传了多少。
	if raw, err := json.Marshal(received); err == nil {
		if err := s.db.Model(&model.UploadSession{}).Where("id = ?", session.ID).
			Update("received_mask", string(raw)).Error; err != nil {
			logx.Warn("更新上传进度失败", "upload_id", uploadID, "err", err)
		}
	}
	return received, nil
}

// Complete 合并分片并落库。
//
// 重复调用是安全的：已经合并过的会话直接把当初生成的节点原样返回，
// 这样前端在网络抖动后重发一次 complete，拿到的还是同一个文件，
// 不会变成报错、更不会多出一份。
func (s *UploadService) Complete(subj *Subject, uploadID string) (*model.Node, error) {
	if node, ok, err := s.completedNode(subj, uploadID); err != nil || ok {
		return node, err
	}
	session, err := s.loadSession(subj, uploadID)
	if err != nil {
		return nil, err
	}
	space, parent, err := s.checkTarget(subj, session.SpaceID, session.ParentID)
	if err != nil {
		return nil, err
	}

	received, err := s.store.ReceivedChunks(uploadID)
	if err != nil {
		return nil, err
	}
	if len(received) < session.ChunkCount {
		return nil, response.BadRequest(fmt.Sprintf("分片未传完，还差 %d 片", session.ChunkCount-len(received)))
	}

	hash, size, err := s.store.MergeChunks(uploadID, session.ChunkCount)
	if err != nil {
		return nil, response.BadRequest(err.Error())
	}
	if session.Hash != "" && session.Hash != hash {
		// 校验不过说明分片错乱或被篡改，清掉让前端重传，别把坏文件写进库。
		_ = s.store.DiscardUpload(uploadID)
		return nil, response.BadRequest("文件校验失败，请重新上传")
	}
	if err := s.space.CheckQuota(space, size); err != nil {
		return nil, err
	}

	plan, err := s.file.PlanConflict(subj, space, session.ParentID, session.Filename,
		ParseConflictMode(session.Conflict))
	if err != nil {
		return nil, err
	}

	var node *model.Node
	err = s.db.Transaction(func(tx *gorm.DB) error {
		n, err := s.file.CreateFile(tx, subj, space, parent, session.Filename, hash, size, plan)
		if err != nil {
			return err
		}
		node = n
		now := time.Now()
		return tx.Model(&model.UploadSession{}).Where("id = ?", session.ID).
			Updates(map[string]any{
				"completed": true, "hash": hash, "node_id": n.ID, "finished_at": now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	if err := s.store.DiscardUpload(uploadID); err != nil {
		logx.Warn("清理上传临时目录失败", "upload_id", uploadID, "err", err)
	}
	return node, nil
}

// Abort 放弃一次分片上传。
func (s *UploadService) Abort(subj *Subject, uploadID string) error {
	session, err := s.loadSession(subj, uploadID)
	if err != nil {
		return err
	}
	if err := s.store.DiscardUpload(uploadID); err != nil {
		return err
	}
	return s.db.Delete(&model.UploadSession{}, session.ID).Error
}

// completedNode 查这个会话是不是已经合并过了。
// 第二个返回值为 true 表示"这次 complete 不用再做了"，节点在第一个返回值里。
//
// 只认自己发起的会话；老会话没记 node_id（或者文件后来被彻底删了）就当没完成过，
// 让原来的流程去报"该上传已完成"，总比返回一个不存在的节点强。
func (s *UploadService) completedNode(subj *Subject, uploadID string) (*model.Node, bool, error) {
	var session model.UploadSession
	err := s.db.Where("upload_id = ?", uploadID).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("查询上传会话失败: %w", err)
	}
	if session.UserID != subj.User.ID || !session.Completed || session.NodeID == 0 {
		return nil, false, nil
	}
	var node model.Node
	if err := s.db.First(&node, session.NodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("查询文件失败: %w", err)
	}
	return &node, true, nil
}

func (s *UploadService) loadSession(subj *Subject, uploadID string) (*model.UploadSession, error) {
	var session model.UploadSession
	if err := s.db.Where("upload_id = ?", uploadID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, response.NotFound("上传会话不存在或已过期")
		}
		return nil, fmt.Errorf("查询上传会话失败: %w", err)
	}
	// 会话只认发起人，防止拿到别人的 uploadID 往别人的目录里塞文件。
	if session.UserID != subj.User.ID {
		return nil, response.Forbidden("无权操作该上传会话")
	}
	if session.Completed {
		return nil, response.BadRequest("该上传已完成")
	}
	if session.ExpireAt.Before(time.Now()) {
		return nil, response.BadRequest("上传会话已过期，请重新上传")
	}
	return &session, nil
}

// CleanupExpired 清理过期的分片上传会话，由后台定时任务调用。
func (s *UploadService) CleanupExpired() (int, error) {
	var sessions []model.UploadSession
	err := s.db.Where("completed = ? AND expire_at < ?", false, time.Now()).Limit(200).Find(&sessions).Error
	if err != nil {
		return 0, fmt.Errorf("查询过期上传会话失败: %w", err)
	}
	for _, session := range sessions {
		if err := s.store.DiscardUpload(session.UploadID); err != nil {
			logx.Warn("清理过期上传目录失败", "upload_id", session.UploadID, "err", err)
		}
		if err := s.db.Delete(&model.UploadSession{}, session.ID).Error; err != nil {
			logx.Warn("删除过期上传会话失败", "upload_id", session.UploadID, "err", err)
		}
	}
	return len(sessions), nil
}
