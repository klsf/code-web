package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

var (
	// 全局日志器
	appLog zerolog.Logger

	// 带上下文的子日志器
	httpLog     zerolog.Logger
	taskLog     zerolog.Logger
	authLog     zerolog.Logger
	storeLog    zerolog.Logger
	providerLog zerolog.Logger
)

// 日志级别
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// LogConfig 日志配置
type LogConfig struct {
	Level      LogLevel // 日志级别
	Format     string   // 输出格式: "console" | "json"
	OutputPath string   // 输出文件路径，空为 stdout
	Pretty     bool     // 是否美化输出（仅 console 格式有效）
}

// DefaultLogConfig 默认配置
var DefaultLogConfig = LogConfig{
	Level:  LevelInfo,
	Format: "console",
	Pretty: true,
}

// initLogger 初始化日志系统
func initLogger(cfg LogConfig) {
	if cfg.Level == "" {
		cfg.Level = DefaultLogConfig.Level
	}
	if cfg.Format == "" {
		cfg.Format = DefaultLogConfig.Format
	}

	var output io.Writer

	// 配置输出
	if cfg.OutputPath != "" {
		file, err := os.OpenFile(cfg.OutputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file: %v, using stdout\n", err)
			output = os.Stdout
		} else {
			output = file
		}
	} else {
		output = os.Stdout
	}

	// 配置美化
	if cfg.Format == "console" {
		consoleWriter := zerolog.ConsoleWriter{Out: output, TimeFormat: time.RFC3339}
		if cfg.Pretty {
			consoleWriter.PartsExclude = []string{zerolog.TimestampFieldName}
		}
		output = consoleWriter
	}

	// 构建日志器
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.TimestampFieldName = "time"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "message"

	appLog = zerolog.New(output).Level(toZerologLevel(cfg.Level)).With().Timestamp().Caller().Logger()

	// 创建带上下文的子日志器
	httpLog = appLog.With().Str("module", "http").Logger()
	taskLog = appLog.With().Str("module", "task").Logger()
	authLog = appLog.With().Str("module", "auth").Logger()
	storeLog = appLog.With().Str("module", "store").Logger()
	providerLog = appLog.With().Str("module", "provider").Logger()
}

// toZerologLevel 转换日志级别
func toZerologLevel(level LogLevel) zerolog.Level {
	switch level {
	case LevelDebug:
		return zerolog.DebugLevel
	case LevelInfo:
		return zerolog.InfoLevel
	case LevelWarn:
		return zerolog.WarnLevel
	case LevelError:
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

// WithContext 创建带上下文的日志器
func WithContext(ctx map[string]interface{}) zerolog.Logger {
	return appLog.With().Fields(ctx).Logger()
}

// HTTP 请求日志包装
type HTTPLog struct {
	Method     string
	Path       string
	StatusCode int
	Duration   time.Duration
	ClientIP   string
	SessionID  string
}

func (h HTTPLog) Log() {
	httpLog.Info().
		Str("method", h.Method).
		Str("path", h.Path).
		Int("status", h.StatusCode).
		Dur("duration", h.Duration).
		Str("client_ip", h.ClientIP).
		Str("session_id", h.SessionID).
		Msg("http request")
}

func (h HTTPLog) LogError(err error) {
	httpLog.Error().
		Str("method", h.Method).
		Str("path", h.Path).
		Int("status", h.StatusCode).
		Dur("duration", h.Duration).
		Str("client_ip", h.ClientIP).
		Str("session_id", h.SessionID).
		Err(err).
		Msg("http request failed")
}

// TaskLog 任务日志
type TaskLog struct {
	SessionID string
	TaskID    string
	Provider  string
	Action    string // "start" | "complete" | "error" | "stop"
	Error     error
	Duration  time.Duration
}

func (t TaskLog) Log() {
	ev := taskLog.Info().
		Str("session_id", t.SessionID).
		Str("task_id", t.TaskID).
		Str("provider", t.Provider).
		Str("action", t.Action)

	if t.Duration > 0 {
		ev.Dur("duration", t.Duration)
	}

	if t.Error != nil {
		ev.Err(t.Error)
	}

	ev.Msg("task status")
}

func (t TaskLog) LogStart() {
	t.Action = "start"
	t.Log()
}

func (t TaskLog) LogComplete() {
	t.Action = "complete"
	t.Log()
}

func (t TaskLog) LogError(err error) {
	t.Error = err
	t.Action = "error"
	t.Log()
}

func (t TaskLog) LogStop() {
	t.Action = "stop"
	t.Log()
}

// AuthLog 认证日志
type AuthLog struct {
	Action    string // "login" | "logout" | "token_validate" | "codex_auth"
	SessionID string
	Success   bool
	ClientIP  string
	Error     error
}

func (a AuthLog) Log() {
	ev := authLog.Info().
		Str("action", a.Action).
		Bool("success", a.Success).
		Str("client_ip", a.ClientIP)

	if a.SessionID != "" {
		ev.Str("session_id", a.SessionID)
	}

	if a.Error != nil {
		ev.Err(a.Error)
	}

	ev.Msg("auth event")
}

func (a AuthLog) LogLogin()         { a.Action = "login"; a.Log() }
func (a AuthLog) LogLogout()        { a.Action = "logout"; a.Log() }
func (a AuthLog) LogTokenValidate() { a.Action = "token_validate"; a.Log() }
func (a AuthLog) LogCodexAuth()     { a.Action = "codex_auth"; a.Log() }

// StoreLog 存储日志
type StoreLog struct {
	SessionID string
	Operation string // "save" | "load" | "delete" | "update"
	Error     error
}

func (s StoreLog) Log() {
	ev := storeLog.Info().
		Str("session_id", s.SessionID).
		Str("operation", s.Operation)

	if s.Error != nil {
		ev.Err(s.Error)
	}

	ev.Msg("store operation")
}

func (s StoreLog) LogSave()           { s.Operation = "save"; s.Log() }
func (s StoreLog) LogLoad()           { s.Operation = "load"; s.Log() }
func (s StoreLog) LogDelete()         { s.Operation = "delete"; s.Log() }
func (s StoreLog) LogError(err error) { s.Error = err; s.Log() }

// ProviderLog Provider 日志
type ProviderLog struct {
	ProviderID string
	Action     string
	Error      error
}

func (p ProviderLog) Log() {
	ev := providerLog.Info().
		Str("provider", p.ProviderID).
		Str("action", p.Action)

	if p.Error != nil {
		ev.Err(p.Error)
	}

	ev.Msg("provider event")
}

func (p ProviderLog) LogError(err error) { p.Error = err; p.Log() }

// ===== 兼容旧 API 的包装 =====

// Log 替代 log.Printf，格式化输出
func Log(format string, args ...interface{}) {
	appLog.Info().Msgf(format, args...)
}

// LogError 错误日志
func LogError(err error, format string, args ...interface{}) {
	appLog.Error().Err(err).Msgf(format, args...)
}

// LogDebug Debug 日志
func LogDebug(format string, args ...interface{}) {
	appLog.Debug().Msgf(format, args...)
}

// LogWarn 警告日志
func LogWarn(format string, args ...interface{}) {
	appLog.Warn().Msgf(format, args...)
}

// LogWithField 带字段的日志
func LogWithField(fields map[string]interface{}, format string, args ...interface{}) {
	logger := WithContext(fields)
	logger.Info().Msgf(format, args...)
}

// ===== 启动时日志 =====

func init() {
	// 默认使用控制台彩色输出
	initLogger(DefaultLogConfig)
}
