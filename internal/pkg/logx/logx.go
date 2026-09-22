// Package logx 提供全局结构化日志。
package logx

import (
	"log/slog"
	"os"
	"strings"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Setup 按配置初始化全局 logger。
func Setup(level, format string) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	logger = slog.New(h)
	slog.SetDefault(logger)
}

// L 返回全局 logger。
func L() *slog.Logger { return logger }

// Debug 输出 debug 日志。
func Debug(msg string, args ...any) { logger.Debug(msg, args...) }

// Info 输出 info 日志。
func Info(msg string, args ...any) { logger.Info(msg, args...) }

// Warn 输出 warn 日志。
func Warn(msg string, args ...any) { logger.Warn(msg, args...) }

// Error 输出 error 日志。
func Error(msg string, args ...any) { logger.Error(msg, args...) }
