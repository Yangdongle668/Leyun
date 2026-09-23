// Command leyun 是乐云企业网盘的服务端入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yangdongle668/Leyun/internal/api"
	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
	"github.com/Yangdongle668/Leyun/internal/pkg/hashx"
	"github.com/Yangdongle668/Leyun/internal/pkg/jwtx"
	"github.com/Yangdongle668/Leyun/internal/pkg/logx"
	"github.com/Yangdongle668/Leyun/internal/service"
	"github.com/Yangdongle668/Leyun/internal/storage"
	"github.com/Yangdongle668/Leyun/internal/store"
)

// version 由构建脚本通过 -ldflags 注入。
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	resetUser := flag.String("reset-password", "",
		"应急：重置指定用户的口令并打印新口令，然后退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("leyun", version)
		return
	}

	if *resetUser != "" {
		if err := resetPassword(*configPath, *resetUser); err != nil {
			logx.Error("重置口令失败", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*configPath); err != nil {
		logx.Error("服务启动失败", "err", err)
		os.Exit(1)
	}
}

// resetPassword 是管理员把自己锁在门外时的应急通道。
//
// 只能在服务器本机、能读到数据库文件的前提下执行，所以不构成远程风险；
// 重置后的账号会被标记为"必须改密"，避免临时口令长期留用。
func resetPassword(configPath, username string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logx.Setup(cfg.Log.Level, cfg.Log.Format)

	db, err := store.Open(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	var user model.User
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		return fmt.Errorf("用户 %q 不存在", username)
	}

	newPassword, err := hashx.RandomToken(6) // 12 位十六进制，够用且好抄
	if err != nil {
		return err
	}
	hashed, err := hashx.HashPassword(newPassword)
	if err != nil {
		return err
	}
	err = db.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{
		"password_hash":        hashed,
		"must_change_password": true,
		"login_failures":       0,
		"locked_until":         nil,
		// 账号被停用过也一并放开，否则重置完还是登不进去。
		"status": model.UserActive,
	}).Error
	if err != nil {
		return fmt.Errorf("写入新口令失败: %w", err)
	}

	line := "════════════════════════════════════════════════════════"
	fmt.Println()
	fmt.Println(line)
	fmt.Println("  口令已重置")
	fmt.Println(line)
	fmt.Printf("  用户名   %s\n", user.Username)
	fmt.Printf("  新口令   %s\n", newPassword)
	fmt.Printf("  角色     %s\n", user.Role.Label())
	fmt.Println(line)
	fmt.Println("  请立即用该口令登录并修改，临时口令不要留用。")
	fmt.Println(line)
	fmt.Println()
	return nil
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logx.Setup(cfg.Log.Level, cfg.Log.Format)
	logx.Info("乐云企业网盘启动中", "version", version, "config", configPath)

	db, err := store.Open(cfg)
	if err != nil {
		return err
	}

	secret, err := store.EnsureJWTSecret(db, cfg.JWT.Secret)
	if err != nil {
		return err
	}
	cfg.JWT.Secret = secret

	seed, err := store.Seed(db, cfg)
	if err != nil {
		return err
	}
	if seed.Created {
		printWelcome(cfg, seed)
	}

	blobStore, err := storage.New(cfg.Storage.Root, cfg.Storage.TempRoot)
	if err != nil {
		return err
	}
	jwtMgr := jwtx.New(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.Expire, cfg.JWT.Refresh)
	svc := service.NewRegistry(db, cfg, blobStore, jwtMgr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go startJanitor(ctx, svc)

	// 知识库索引在后台慢慢跑。没配大模型时它每轮直接返回，不占资源。
	svc.KB.Start()
	defer svc.KB.Stop()

	router := api.NewRouter(cfg, svc)
	srv := &http.Server{
		Addr:    cfg.Addr(),
		Handler: router,
		// 上传大文件可能持续很久，读写超时交给反向代理与客户端控制，这里只限制头部。
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logx.Info("HTTP 服务已就绪", "addr", cfg.Addr(), "office", cfg.Office.Enabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// 绑了域名就再起一组 80/443 监听。两者共存而不是二选一：
	// 反向代理后面的部署仍然走原来的端口，直接对公网的部署才用得上这一组。
	tlsSrv, acmeSrv := startAutoTLS(svc, router)
	defer shutdownQuietly(tlsSrv, acmeSrv)

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case <-ctx.Done():
		logx.Info("收到退出信号，正在优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	logx.Info("已安全退出")
	return nil
}

// startAutoTLS 在绑定了域名时，额外监听 443（HTTPS）与 80（ACME 验证）。
//
// 没绑域名就什么都不做——绝大多数部署在反向代理后面，证书由 Nginx 管，
// 这时去抢 80/443 只会启动失败。
func startAutoTLS(svc *service.Registry, handler http.Handler) (*http.Server, *http.Server) {
	tlsConf := svc.TLS.TLSConfigFor()
	if tlsConf == nil {
		return nil, nil
	}
	cfg := svc.TLS.Config()
	mgr := svc.TLS.Manager()

	tlsSrv := &http.Server{
		Addr:              ":443",
		Handler:           handler,
		TLSConfig:         tlsConf,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		logx.Info("HTTPS 服务已就绪", "domains", cfg.Domains)
		// 证书由 TLSConfig 里的 autocert 现取，所以证书与私钥路径都传空。
		if err := tlsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 监听失败不该让整个进程退出：80/443 被占或没有权限是很常见的，
			// 这时原来的端口仍然可用，管理员还能进后台把配置改回去。
			logx.Error("HTTPS 监听失败，请检查 443 端口是否被占用或缺少权限", "err", err)
		}
	}()

	// 80 端口必须留着：HTTP-01 验证就走它，而且要让用户输 http:// 也能进来。
	acmeSrv := &http.Server{
		Addr:              ":80",
		Handler:           mgr.HTTPHandler(redirectOrServe(svc, handler)),
		ReadHeaderTimeout: 20 * time.Second,
	}
	go func() {
		if err := acmeSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logx.Error("80 端口监听失败，证书将无法通过 HTTP-01 方式验证", "err", err)
		}
	}()
	return tlsSrv, acmeSrv
}

// redirectOrServe 决定 80 端口上的普通请求怎么处理。
//
// mgr.HTTPHandler 会先把 ACME 验证路径截走，剩下的才轮到这里。
func redirectOrServe(svc *service.Registry, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !svc.TLS.Config().Redirect {
			handler.ServeHTTP(w, r)
			return
		}
		target := "https://" + stripPort(r.Host) + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func shutdownQuietly(servers ...*http.Server) {
	for _, srv := range servers {
		if srv == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
}

// startJanitor 定时清理过期的分片上传会话。
func startJanitor(ctx context.Context, svc *service.Registry) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := svc.Upload.CleanupExpired()
			if err != nil {
				logx.Warn("清理过期上传会话失败", "err", err)
				continue
			}
			if n > 0 {
				logx.Info("已清理过期上传会话", "count", n)
			}
		}
	}
}

// printWelcome 首次初始化时把默认账号醒目地打出来。
func printWelcome(cfg *config.Config, seed *store.SeedResult) {
	line := "════════════════════════════════════════════════════════"
	fmt.Println()
	fmt.Println(line)
	fmt.Println("  乐云企业网盘 · 初始化完成")
	fmt.Println(line)
	fmt.Printf("  访问地址   http://%s\n", cfg.Addr())
	fmt.Printf("  默认账号   %s\n", seed.AdminUsername)
	fmt.Printf("  默认口令   %s\n", seed.AdminPassword)
	fmt.Printf("  顶级部门   %s\n", seed.RootDepartmentName)
	fmt.Println(line)
	fmt.Println("  该账号是系统中唯一可以开通其他账号的超级管理员。")
	fmt.Println("  系统不提供自助注册，请登录后在「管理后台 → 账号管理」中开通成员账号。")
	fmt.Println("  ⚠ 默认口令非常弱，请立即在「个人设置 → 修改口令」中更换。")
	fmt.Println(line)
	fmt.Println()
}
