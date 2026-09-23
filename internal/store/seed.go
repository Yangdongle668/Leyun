package store

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/treex"
)

// 系统配置键。
const (
	// SettingInitialized 标记系统是否已完成初始化。
	SettingInitialized = "system.initialized"
	// SettingSiteName 站点名称，显示在登录页与侧边栏。
	SettingSiteName = "system.site_name"
	// SettingDefaultUserQuota 新账号默认个人空间配额（字节）。
	SettingDefaultUserQuota = "system.default_user_quota"
	// SettingDefaultDeptQuota 新部门默认空间配额（字节）。
	SettingDefaultDeptQuota = "system.default_dept_quota"
	// SettingAllowPublicShare 是否允许创建对外公开分享。
	SettingAllowPublicShare = "system.allow_public_share"
	// SettingTrashRetentionDays 回收站保留天数，0 表示不自动清理。
	SettingTrashRetentionDays = "system.trash_retention_days"
	// SettingRootDeptID 根部门 ID。
	SettingRootDeptID = "system.root_dept_id"
	// SettingPublicSpaceID 公共空间 ID。
	SettingPublicSpaceID = "system.public_space_id"
	// SettingJWTSecret 自动生成的令牌签名密钥，保证重启后登录态不失效。
	SettingJWTSecret = "system.jwt_secret"
)

// 知识库（AI 问答）配置键。
//
// 这些项放在数据库而不是配置文件里，是因为用户要求在管理页面上配：
// 换模型、换供应商不该需要登服务器改 yaml 再重启。
const (
	// SettingAIEnabled 知识库总开关。关掉后索引停止、问答入口消失。
	SettingAIEnabled = "ai.enabled"
	// SettingAIBaseURL 大模型服务地址，需兼容 OpenAI 接口（/v1/chat/completions、/v1/embeddings）。
	// 通义、智谱、DeepSeek、Moonshot 以及自建的 Ollama / Xinference / vLLM 都满足。
	SettingAIBaseURL = "ai.base_url"
	// SettingAIAPIKey 大模型的 API Key。属于秘密，只进不出。
	SettingAIAPIKey = "ai.api_key"
	// SettingAIChatModel 回答用的对话模型名。
	SettingAIChatModel = "ai.chat_model"
	// SettingAIEmbedModel 建索引用的向量模型名。
	SettingAIEmbedModel = "ai.embed_model"
	// SettingAIEmbedDim 向量维度。与模型不符时索引建不起来，所以显式记下来，
	// 换模型导致维度变化时要整体重建。
	SettingAIEmbedDim = "ai.embed_dim"
	// SettingAIEmbedBatch 一次向量请求最多送几条文本。留空或填 0 表示自动：
	// 从一个保守值起步，撞上服务商的上限就自动退让。知道自家上限的可以直接填，
	// 填大了也不会出错，只是第一批会先撞一次墙再退回来。
	SettingAIEmbedBatch = "ai.embed_batch"
	// SettingAIChunkSize 切块大小（字符数）。
	SettingAIChunkSize = "ai.chunk_size"
	// SettingAIChunkOverlap 相邻块的重叠字符数，避免答案正好被切断在边界上。
	SettingAIChunkOverlap = "ai.chunk_overlap"
	// SettingAITopK 最终喂给模型的片段数。
	SettingAITopK = "ai.top_k"
	// SettingAISpaceIDs 参与索引的空间白名单（逗号分隔），为空表示全部非个人空间。
	SettingAISpaceIDs = "ai.space_ids"
	// SettingAIIncludePersonal 是否把个人空间也纳入索引。默认否——
	// 个人空间是员工的私人区域，检索时虽然会被权限过滤挡住，
	// 但没必要让它多一份脱离 ACL 的向量副本。
	SettingAIIncludePersonal = "ai.include_personal"
	// SettingAIMaxFileSize 单个文件参与索引的大小上限（字节），超过直接跳过。
	SettingAIMaxFileSize = "ai.max_file_size"
)

// 域名与证书配置键。
//
// 同样放数据库：绑域名、换域名属于日常运维，不该每次都登服务器改 yaml。
const (
	// SettingTLSEnabled 是否启用自动 HTTPS。
	SettingTLSEnabled = "tls.enabled"
	// SettingTLSDomains 绑定的域名（逗号分隔）。证书只会为这些域名签发。
	SettingTLSDomains = "tls.domains"
	// SettingTLSEmail ACME 账号联系邮箱，证书将到期时签发机构会往这里发提醒。
	SettingTLSEmail = "tls.email"
	// SettingTLSDirectoryURL ACME 目录地址，留空用 Let's Encrypt 正式环境。
	// 调试时填测试环境，避免把正式环境的签发频率限额用光。
	SettingTLSDirectoryURL = "tls.directory_url"
	// SettingTLSRedirect 是否把 HTTP 请求整体跳转到 HTTPS。
	SettingTLSRedirect = "tls.redirect"
	// SettingTLSAgreedAt 记录管理员同意签发机构服务条款的时间。
	// ACME 协议要求明示同意，所以要有人点过这一下才允许申请。
	SettingTLSAgreedAt = "tls.agreed_at"
)

// EnsureJWTSecret 返回令牌签名密钥。
//
// 配置里没写就自动生成一个并存进数据库——这样用户不配置也能安全启动，
// 且重启后已登录的会话不会全部掉线。
func EnsureJWTSecret(db *gorm.DB, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	var item model.Setting
	err := db.Where("config_key = ?", SettingJWTSecret).First(&item).Error
	if err == nil && item.Value != "" {
		return item.Value, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("读取令牌密钥失败: %w", err)
	}
	secret, err := hashx.RandomToken(32)
	if err != nil {
		return "", err
	}
	item = model.Setting{
		Key:       SettingJWTSecret,
		Value:     secret,
		Remark:    "自动生成的令牌签名密钥，请勿手工修改",
		UpdatedAt: time.Now(),
	}
	if err := db.Save(&item).Error; err != nil {
		return "", fmt.Errorf("保存令牌密钥失败: %w", err)
	}
	logx.Info("已自动生成令牌签名密钥并保存到数据库")
	return secret, nil
}

// SeedResult 描述初始化结果，供启动日志打印。
type SeedResult struct {
	Created            bool
	AdminUsername      string
	AdminPassword      string
	RootDepartmentID   uint64
	RootDepartmentName string
	PublicSpaceID      uint64
}

// Seed 在首次启动时写入初始数据：
//
//  1. 根部门（公司）；
//  2. 公共空间，并对全员授予只读权限；
//  3. 默认超级管理员账号（默认 admin/admin）及其个人空间。
//
// 该函数幂等：已初始化过的库再次调用不会重复写入。
func Seed(db *gorm.DB, cfg *config.Config) (*SeedResult, error) {
	res := &SeedResult{
		AdminUsername: cfg.Security.DefaultAdminUsername,
		AdminPassword: cfg.Security.DefaultAdminPassword,
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		var adminCount int64
		if err := tx.Model(&model.User{}).Where("role = ?", model.RoleSuperAdmin).Count(&adminCount).Error; err != nil {
			return fmt.Errorf("统计超级管理员失败: %w", err)
		}
		if adminCount > 0 {
			// 已有超管，说明系统初始化过；只补齐可能缺失的系统配置。
			return ensureSettings(tx, cfg, 0, 0)
		}

		// 1. 根部门。
		rootDept := &model.Department{
			Name:     "总公司",
			Code:     "ROOT",
			ParentID: 0,
			Depth:    0,
			Sort:     0,
			Enabled:  true,
			Remark:   "系统初始化创建的顶级部门，可在部门管理中改名",
		}
		if err := tx.Create(rootDept).Error; err != nil {
			return fmt.Errorf("创建根部门失败: %w", err)
		}
		rootDept.Path = treex.Build(treex.Root, rootDept.ID)
		if err := tx.Model(rootDept).Update("path", rootDept.Path).Error; err != nil {
			return fmt.Errorf("回写根部门路径失败: %w", err)
		}
		res.RootDepartmentID = rootDept.ID
		res.RootDepartmentName = rootDept.Name

		// 2. 公共空间：全员默认只读，需要谁能传文件再单独授权。
		publicSpace := &model.Space{
			Type:       model.SpacePublic,
			Name:       "公共空间",
			QuotaBytes: 0,
			Enabled:    true,
		}
		if err := tx.Create(publicSpace).Error; err != nil {
			return fmt.Errorf("创建公共空间失败: %w", err)
		}
		res.PublicSpaceID = publicSpace.ID
		everyoneRule := &model.AccessRule{
			SpaceID:       publicSpace.ID,
			NodeID:        0,
			PrincipalType: model.PrincipalEveryone,
			Allow:         model.PermReadOnly,
			Inheritable:   true,
			Remark:        "全员可浏览下载公共空间",
		}
		if err := tx.Create(everyoneRule).Error; err != nil {
			return fmt.Errorf("初始化公共空间权限失败: %w", err)
		}

		// 3. 默认超级管理员。
		hashed, err := hashx.HashPassword(cfg.Security.DefaultAdminPassword)
		if err != nil {
			return err
		}
		admin := &model.User{
			Username:     cfg.Security.DefaultAdminUsername,
			PasswordHash: hashed,
			Nickname:     "超级管理员",
			Role:         model.RoleSuperAdmin,
			Status:       model.UserActive,
			DeptID:       rootDept.ID,
			QuotaBytes:   0,
			// 默认口令与用户名同样简单，标记出来让前端持续提醒改密。
			MustChangePassword: true,
			Remark:             "系统初始化账号",
		}
		if err := tx.Create(admin).Error; err != nil {
			return fmt.Errorf("创建超级管理员失败: %w", err)
		}
		if err := tx.Model(admin).Update("created_by", admin.ID).Error; err != nil {
			return fmt.Errorf("回写超管创建人失败: %w", err)
		}
		personal := &model.Space{
			Type:       model.SpacePersonal,
			Name:       admin.Nickname + "的空间",
			OwnerID:    admin.ID,
			QuotaBytes: 0,
			Enabled:    true,
		}
		if err := tx.Create(personal).Error; err != nil {
			return fmt.Errorf("创建超管个人空间失败: %w", err)
		}

		// 根部门空间。
		deptSpace := &model.Space{
			Type:       model.SpaceDepartment,
			Name:       rootDept.Name,
			DeptID:     rootDept.ID,
			QuotaBytes: cfg.Security.DefaultDeptQuota,
			Enabled:    true,
		}
		if err := tx.Create(deptSpace).Error; err != nil {
			return fmt.Errorf("创建根部门空间失败: %w", err)
		}
		deptRule := &model.AccessRule{
			SpaceID:        deptSpace.ID,
			NodeID:         0,
			PrincipalType:  model.PrincipalDept,
			PrincipalID:    rootDept.ID,
			Allow:          model.PermCollaborate,
			IncludeSubDept: true,
			Inheritable:    true,
			Remark:         "本部门及子部门成员默认可读写并可分享",
		}
		if err := tx.Create(deptRule).Error; err != nil {
			return fmt.Errorf("初始化根部门空间权限失败: %w", err)
		}

		res.Created = true
		return ensureSettings(tx, cfg, rootDept.ID, publicSpace.ID)
	})
	if err != nil {
		return nil, err
	}

	if res.Created {
		logx.Warn("系统已完成初始化，请立即修改默认口令",
			"username", res.AdminUsername, "password", res.AdminPassword)
	}
	return res, nil
}

func ensureSettings(tx *gorm.DB, cfg *config.Config, rootDeptID, publicSpaceID uint64) error {
	defaults := map[string][2]string{
		SettingInitialized:        {"true", "系统是否已初始化"},
		SettingSiteName:           {"乐云企业网盘", "站点名称"},
		SettingDefaultUserQuota:   {fmt.Sprintf("%d", cfg.Security.DefaultUserQuota), "新账号默认个人空间配额(字节),0 为不限"},
		SettingDefaultDeptQuota:   {fmt.Sprintf("%d", cfg.Security.DefaultDeptQuota), "新部门默认空间配额(字节),0 为不限"},
		SettingAllowPublicShare:   {"true", "是否允许创建对外公开分享链接"},
		SettingTrashRetentionDays: {fmt.Sprintf("%d", int(cfg.Storage.TrashRetention.Hours()/24)), "回收站保留天数,0 为不自动清理"},
	}
	if rootDeptID > 0 {
		defaults[SettingRootDeptID] = [2]string{fmt.Sprintf("%d", rootDeptID), "根部门 ID"}
	}
	if publicSpaceID > 0 {
		defaults[SettingPublicSpaceID] = [2]string{fmt.Sprintf("%d", publicSpaceID), "公共空间 ID"}
	}

	for key, val := range defaults {
		var existing model.Setting
		err := tx.Where("config_key = ?", key).First(&existing).Error
		switch {
		case err == nil:
			continue
		case errors.Is(err, gorm.ErrRecordNotFound):
			item := model.Setting{Key: key, Value: val[0], Remark: val[1], UpdatedAt: time.Now()}
			if err := tx.Create(&item).Error; err != nil {
				return fmt.Errorf("写入系统配置 %s 失败: %w", key, err)
			}
		default:
			return fmt.Errorf("读取系统配置 %s 失败: %w", key, err)
		}
	}
	return nil
}
