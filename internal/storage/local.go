// Package storage 实现基于本地磁盘的内容寻址存储。
//
// 同一份内容只落盘一次，路径由 SHA-256 决定（root/ab/cd/<hash>），
// 谁在用由数据库里的 Blob.RefCount 记账。这样"秒传"和"删一份不影响另一份"都是自然结果。
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
)

// Store 是本地磁盘存储。
type Store struct {
	root string
	temp string
}

// New 创建存储并确保目录存在。
func New(root, temp string) (*Store, error) {
	for _, dir := range []string{root, temp, filepath.Join(temp, "uploads")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("创建存储目录 %s 失败: %w", dir, err)
		}
	}
	return &Store{root: root, temp: temp}, nil
}

// Root 返回 blob 根目录。
func (s *Store) Root() string { return s.root }

// blobPath 由哈希推导出落盘路径，两级散列目录避免单目录塞进几十万个文件。
func (s *Store) blobPath(hash string) (string, error) {
	if len(hash) < 4 || !isHex(hash) {
		return "", fmt.Errorf("非法的内容哈希: %q", hash)
	}
	return filepath.Join(s.root, hash[0:2], hash[2:4], hash), nil
}

// Path 返回内容的绝对路径。
func (s *Store) Path(hash string) (string, error) { return s.blobPath(hash) }

// Exists 判断内容是否已落盘。
func (s *Store) Exists(hash string) bool {
	p, err := s.blobPath(hash)
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Open 打开内容用于读取（支持 Seek，配合 HTTP Range 做断点下载与视频拖动）。
func (s *Store) Open(hash string) (*os.File, error) {
	p, err := s.blobPath(hash)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("打开文件内容失败: %w", err)
	}
	return f, nil
}

// Put 把 r 的内容写入存储，返回内容哈希与字节数。
//
// 先写临时文件并同步计算哈希，再按哈希重命名到最终位置；
// 目标已存在说明命中去重，直接丢弃临时文件。
func (s *Store) Put(r io.Reader) (hash string, size int64, err error) {
	tmp, err := os.CreateTemp(s.temp, "put-*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	h := hashx.NewContentHasher()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("刷新临时文件失败: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("关闭临时文件失败: %w", err)
	}
	hash = hashx.Sum(h)
	if err = s.adopt(tmpName, hash); err != nil {
		return "", 0, err
	}
	return hash, size, nil
}

// adopt 把已完成的临时文件挪到内容寻址位置。
func (s *Store) adopt(tmpName, hash string) error {
	dst, err := s.blobPath(hash)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		// 已有同样内容，直接复用。
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("创建内容目录失败: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		// 跨文件系统时 rename 会失败，回退到拷贝。
		if copyErr := copyFile(tmpName, dst); copyErr != nil {
			return fmt.Errorf("保存文件内容失败: %w", copyErr)
		}
	}
	return os.Chmod(dst, 0o640)
}

// Remove 删除内容。调用方需自行保证引用计数已归零。
func (s *Store) Remove(hash string) error {
	p, err := s.blobPath(hash)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除文件内容失败: %w", err)
	}
	return nil
}

// uploadDir 返回某次分片上传的临时目录。
func (s *Store) uploadDir(uploadID string) (string, error) {
	if uploadID == "" || strings.ContainsAny(uploadID, `/\.`) {
		return "", fmt.Errorf("非法的上传会话号")
	}
	return filepath.Join(s.temp, "uploads", uploadID), nil
}

// PrepareUpload 为一次分片上传准备目录。
func (s *Store) PrepareUpload(uploadID string) error {
	dir, err := s.uploadDir(uploadID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("创建上传目录失败: %w", err)
	}
	return nil
}

// WriteChunk 落盘一个分片，返回分片字节数。
func (s *Store) WriteChunk(uploadID string, index int, r io.Reader) (int64, error) {
	dir, err := s.uploadDir(uploadID)
	if err != nil {
		return 0, err
	}
	if index < 0 {
		return 0, fmt.Errorf("非法的分片序号: %d", index)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, fmt.Errorf("创建上传目录失败: %w", err)
	}
	// 先写 .part 再改名，避免中断留下半截分片被误判为已完成。
	partial := filepath.Join(dir, strconv.Itoa(index)+".part")
	f, err := os.Create(partial)
	if err != nil {
		return 0, fmt.Errorf("创建分片文件失败: %w", err)
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		os.Remove(partial)
		return 0, fmt.Errorf("写入分片失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(partial)
		return 0, fmt.Errorf("刷新分片失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(partial)
		return 0, fmt.Errorf("关闭分片失败: %w", err)
	}
	final := filepath.Join(dir, strconv.Itoa(index)+".chunk")
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return 0, fmt.Errorf("提交分片失败: %w", err)
	}
	return n, nil
}

// ReceivedChunks 扫描磁盘上已完成的分片序号，用于断点续传时告诉前端"还差哪几片"。
func (s *Store) ReceivedChunks(uploadID string) ([]int, error) {
	dir, err := s.uploadDir(uploadID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取上传目录失败: %w", err)
	}
	out := make([]int, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".chunk") {
			continue
		}
		if idx, err := strconv.Atoi(strings.TrimSuffix(name, ".chunk")); err == nil {
			out = append(out, idx)
		}
	}
	sort.Ints(out)
	return out, nil
}

// MergeChunks 按序号顺序合并分片并写入内容寻址存储，返回哈希与总字节数。
func (s *Store) MergeChunks(uploadID string, chunkCount int) (string, int64, error) {
	dir, err := s.uploadDir(uploadID)
	if err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(s.temp, "merge-*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("创建合并临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	h := hashx.NewContentHasher()
	w := io.MultiWriter(tmp, h)
	var total int64
	for i := 0; i < chunkCount; i++ {
		part := filepath.Join(dir, strconv.Itoa(i)+".chunk")
		f, err := os.Open(part)
		if err != nil {
			return "", 0, fmt.Errorf("缺少第 %d 个分片: %w", i+1, err)
		}
		n, err := io.Copy(w, f)
		f.Close()
		if err != nil {
			return "", 0, fmt.Errorf("合并第 %d 个分片失败: %w", i+1, err)
		}
		total += n
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("刷新合并文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("关闭合并文件失败: %w", err)
	}
	hash := hashx.Sum(h)
	if err := s.adopt(tmpName, hash); err != nil {
		return "", 0, err
	}
	return hash, total, nil
}

// DiscardUpload 清理一次分片上传的临时目录。
func (s *Store) DiscardUpload(uploadID string) error {
	dir, err := s.uploadDir(uploadID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("清理上传目录失败: %w", err)
	}
	return nil
}

// Usage 返回存储目录已占用的字节数（遍历统计，仅用于管理后台展示）。
func (s *Store) Usage() (int64, error) {
	var total int64
	err := filepath.WalkDir(s.root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("统计存储占用失败: %w", err)
	}
	return total, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
