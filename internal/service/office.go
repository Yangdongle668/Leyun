package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/jwtx"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/pkg/response"
)

// 资源令牌的用途。
const (
	// ScopeOfficeRead 允许 Document Server 拉取文件内容。
	ScopeOfficeRead = "office.read"
	// ScopeOfficeWrite 允许 Document Server 回调保存。
	ScopeOfficeWrite = "office.write"
)

// officeDocTypes 把扩展名映射到 ONLYOFFICE 的三类编辑器。
var officeDocTypes = map[string]string{
	"doc": "word", "docx": "word", "docm": "word", "dot": "word", "dotx": "word",
	"odt": "word", "ott": "word", "rtf": "word", "txt": "word", "fodt": "word",
	"xls": "cell", "xlsx": "cell", "xlsm": "cell", "xlt": "cell", "xltx": "cell",
	"ods": "cell", "ots": "cell", "csv": "cell", "fods": "cell",
	"ppt": "slide", "pptx": "slide", "pptm": "slide", "pot": "slide", "potx": "slide",
	"odp": "slide", "otp": "slide", "fodp": "slide",
}

// officeEditableExts 是能够直接保存回原格式的扩展名。
// 其余格式（doc/xls/ppt 等旧二进制格式与 csv/txt）只提供只读预览，避免转存时丢格式。
var officeEditableExts = map[string]bool{
	"docx": true, "xlsx": true, "pptx": true,
	"odt": true, "ods": true, "odp": true,
	"txt": true, "csv": true,
}

// IsOfficeDocument 判断该文件能否用在线 Office 打开（含只读预览）。
func IsOfficeDocument(name string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	_, ok := officeDocTypes[ext]
	return ok
}

// OfficeService 对接 ONLYOFFICE Document Server。
type OfficeService struct {
	cfg    *config.Config
	jwt    *jwtx.Manager
	file   *FileService
	acl    *ACLService
	space  *SpaceService
	audit  *AuditService
	client *http.Client
}

// NewOfficeService 构造 Office 服务。
func NewOfficeService(cfg *config.Config, jwtMgr *jwtx.Manager, file *FileService, acl *ACLService, space *SpaceService, audit *AuditService) *OfficeService {
	return &OfficeService{
		cfg: cfg, jwt: jwtMgr, file: file, acl: acl, space: space, audit: audit,
		client: &http.Client{Timeout: 2 * time.Minute},
	}
}

// Enabled 返回是否已开启在线编辑。
func (s *OfficeService) Enabled() bool { return s.cfg.Office.Enabled }

// EditorConfig 是下发给前端的编辑器配置。
type EditorConfig struct {
	// ServerURL 前端据此加载 Document Server 的 api.js。
	ServerURL string         `json:"server_url"`
	Config    map[string]any `json:"config"`
	// Mode 为 edit 或 view，前端用来决定标题栏提示。
	Mode     string `json:"mode"`
	FileName string `json:"file_name"`
}

// BuildConfig 为一个文件生成编辑器配置。
func (s *OfficeService) BuildConfig(subj *Subject, spaceID, nodeID uint64, callbackBase string) (*EditorConfig, error) {
	if !s.cfg.Office.Enabled {
		return nil, response.BadRequest("系统未开启 Office 在线编辑")
	}
	space, err := s.space.Get(spaceID)
	if err != nil {
		return nil, err
	}
	node, err := s.file.GetNode(spaceID, nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil || node.IsDir {
		return nil, response.BadRequest("请选择一个文档文件")
	}
	if node.Trashed {
		return nil, response.BadRequest("回收站中的文件不能编辑")
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(node.Name), "."))
	docType, ok := officeDocTypes[ext]
	if !ok {
		return nil, response.BadRequest("该文件类型不支持在线编辑")
	}

	perm, err := s.acl.Require(subj, space, node, model.PermView)
	if err != nil {
		return nil, err
	}
	// 能不能改，完全跟着 ACL 走：只有 edit 权限且格式支持保存时才开放编辑模式。
	canEdit := perm.Has(model.PermEdit) && officeEditableExts[ext]
	canDownload := perm.Has(model.PermDownload)

	base := s.callbackBase(callbackBase)
	readToken, err := s.jwt.IssueResource(node.ID, subj.User.ID, ScopeOfficeRead, s.cfg.Office.TokenTTL)
	if err != nil {
		return nil, err
	}
	fileURL := fmt.Sprintf("%s/api/v1/office/content?token=%s", base, url.QueryEscape(readToken))

	displayName := subj.User.Nickname
	if displayName == "" {
		displayName = subj.User.Username
	}

	doc := map[string]any{
		"fileType": ext,
		// key 必须随内容变化，否则 Document Server 会拿缓存里的旧版本。
		"key":   fmt.Sprintf("n%d-v%d-%s", node.ID, node.Version, shortHash(node.BlobHash)),
		"title": node.Name,
		"url":   fileURL,
		"permissions": map[string]any{
			"edit":                 canEdit,
			"download":             canDownload,
			"print":                canDownload,
			"copy":                 canDownload,
			"review":               canEdit,
			"comment":              canEdit,
			"fillForms":            canEdit,
			"modifyFilter":         canEdit,
			"modifyContentControl": canEdit,
		},
	}

	editorCfg := map[string]any{
		"lang": s.cfg.Office.Lang,
		"mode": modeOf(canEdit),
		"user": map[string]any{
			"id":   fmt.Sprintf("%d", subj.User.ID),
			"name": displayName,
		},
		"customization": map[string]any{
			"autosave":      true,
			"forcesave":     true,
			"compactHeader": true,
			"toolbarNoTabs": false,
			"hideRightMenu": false,
			"uiTheme":       "theme-classic-light",
			"chat":          false,
			"comments":      canEdit,
			"help":          false,
			"feedback":      false,
		},
	}
	if canEdit {
		writeToken, err := s.jwt.IssueResource(node.ID, subj.User.ID, ScopeOfficeWrite, s.cfg.Office.TokenTTL)
		if err != nil {
			return nil, err
		}
		editorCfg["callbackUrl"] = fmt.Sprintf("%s/api/v1/office/callback?token=%s", base, url.QueryEscape(writeToken))
	}

	cfg := map[string]any{
		"document":     doc,
		"documentType": docType,
		"editorConfig": editorCfg,
		"type":         "desktop",
		"width":        "100%",
		"height":       "100%",
	}

	// Document Server 开启 JWT 校验时，整份配置要用它的密钥签一次。
	if s.cfg.Office.JWTSecret != "" {
		signed, err := s.signPayload(cfg)
		if err != nil {
			return nil, err
		}
		cfg["token"] = signed
	}

	return &EditorConfig{
		ServerURL: s.cfg.Office.PublicURL,
		Config:    cfg,
		Mode:      modeOf(canEdit),
		FileName:  node.Name,
	}, nil
}

func modeOf(canEdit bool) string {
	if canEdit {
		return "edit"
	}
	return "view"
}

func (s *OfficeService) signPayload(payload map[string]any) (string, error) {
	claims := jwt.MapClaims{}
	for k, v := range payload {
		claims[k] = v
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.Office.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("签名编辑器配置失败: %w", err)
	}
	return signed, nil
}

func (s *OfficeService) callbackBase(requestBase string) string {
	if s.cfg.Office.CallbackBase != "" {
		return s.cfg.Office.CallbackBase
	}
	return strings.TrimRight(requestBase, "/")
}

// ResolveContentToken 校验读取令牌并返回对应文件。
func (s *OfficeService) ResolveContentToken(token string) (*model.Node, error) {
	claims, err := s.jwt.ParseResource(token, ScopeOfficeRead)
	if err != nil {
		return nil, response.Unauthorized("文档访问令牌无效或已过期")
	}
	node, err := s.file.GetNode(0, claims.NodeID)
	if err != nil {
		return nil, err
	}
	if node == nil || node.IsDir {
		return nil, response.NotFound("文件不存在")
	}
	return node, nil
}

// CallbackBody 是 Document Server 回调的报文。
type CallbackBody struct {
	Key    string   `json:"key"`
	Status int      `json:"status"`
	URL    string   `json:"url"`
	Users  []string `json:"users"`
	Error  int      `json:"error"`
}

// Document Server 回调状态码。
const (
	callbackEditing        = 1
	callbackReadyForSave   = 2
	callbackSaveError      = 3
	callbackClosedNoChange = 4
	callbackForceSave      = 6
	callbackForceSaveErr   = 7
)

// HandleCallback 处理 Document Server 的保存回调。
//
// status=2（编辑结束）与 status=6（强制保存）时，把编辑后的文档拉回来写成新版本。
func (s *OfficeService) HandleCallback(token, authHeader string, body []byte) error {
	claims, err := s.jwt.ParseResource(token, ScopeOfficeWrite)
	if err != nil {
		return response.Unauthorized("保存令牌无效或已过期")
	}

	var cb CallbackBody
	if err := json.Unmarshal(body, &cb); err != nil {
		return response.BadRequest("回调报文格式错误")
	}
	// Document Server 开了 JWT 就一定会带签名，此时必须验签，否则任何人都能伪造保存。
	if s.cfg.Office.JWTSecret != "" {
		if err := s.verifyCallbackSignature(authHeader, &cb); err != nil {
			return err
		}
	}

	switch cb.Status {
	case callbackEditing, callbackClosedNoChange:
		return nil
	case callbackSaveError, callbackForceSaveErr:
		logx.Error("Office 保存失败", "node_id", claims.NodeID, "status", cb.Status)
		return nil
	case callbackReadyForSave, callbackForceSave:
	default:
		return nil
	}
	if cb.URL == "" {
		return nil
	}

	node, err := s.file.GetNode(0, claims.NodeID)
	if err != nil {
		return err
	}
	if node == nil || node.IsDir {
		return response.NotFound("文件不存在")
	}

	reader, err := s.fetchEditedFile(cb.URL)
	if err != nil {
		return err
	}
	defer reader.Close()

	hash, size, err := s.file.store.Put(reader)
	if err != nil {
		return err
	}
	editorUserID := claims.UserID
	if len(cb.Users) > 0 {
		if id := parseUint(cb.Users[0]); id > 0 {
			editorUserID = id
		}
	}
	if err := s.file.UpdateContent(editorUserID, node.ID, hash, size); err != nil {
		return err
	}

	s.audit.Write(Entry{
		UserID: editorUserID, Action: ActionFileUpload, TargetType: "node", TargetID: node.ID,
		Target: node.Name, Detail: "Office 在线编辑保存", Success: true,
	})
	logx.Info("Office 文档已保存", "node_id", node.ID, "size", size)
	return nil
}

func (s *OfficeService) verifyCallbackSignature(authHeader string, cb *CallbackBody) error {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if raw == "" {
		return response.Unauthorized("回调缺少签名")
	}
	parsed, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("不支持的签名算法")
		}
		return []byte(s.cfg.Office.JWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return response.Unauthorized("回调签名校验失败")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return response.Unauthorized("回调签名内容异常")
	}
	// Document Server 把整个回调体包在 payload 里签名，以签名里的内容为准。
	payload, ok := claims["payload"].(map[string]any)
	if !ok {
		payload = claims
	}
	if u, ok := payload["url"].(string); ok && u != "" {
		cb.URL = u
	}
	if st, ok := payload["status"].(float64); ok {
		cb.Status = int(st)
	}
	return nil
}

// fetchEditedFile 从 Document Server 取回编辑后的文档。
//
// 回调里的 URL 来自外部服务，直接拿去请求等于把后端变成跳板；
// 这里只允许它指向配置好的 Document Server 主机。
func (s *OfficeService) fetchEditedFile(rawURL string) (io.ReadCloser, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, response.BadRequest("回调地址不合法")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, response.BadRequest("回调地址协议不受支持")
	}
	if !s.allowedHost(target.Host) {
		logx.Warn("拒绝来自非信任主机的 Office 回调地址", "host", target.Host)
		return nil, response.Forbidden("回调地址不在信任范围内")
	}

	resp, err := s.client.Get(target.String())
	if err != nil {
		return nil, fmt.Errorf("拉取编辑结果失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("拉取编辑结果失败: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *OfficeService) allowedHost(host string) bool {
	hosts := make([]string, 0, 2)
	for _, raw := range []string{s.cfg.Office.InternalURL, s.cfg.Office.PublicURL} {
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return true
		}
		// Document Server 回调里常带容器内部端口，主机名相同即视为同一服务。
		if strings.EqualFold(hostOnly(h), hostOnly(host)) {
			return true
		}
	}
	return false
}

func hostOnly(hostPort string) string {
	if i := strings.LastIndex(hostPort, ":"); i > 0 {
		return hostPort[:i]
	}
	return hostPort
}

func shortHash(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	if h == "" {
		return "new"
	}
	return h
}

func parseUint(s string) uint64 {
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}
