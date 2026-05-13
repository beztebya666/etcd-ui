package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(service string) *zap.Logger {
	level := zapcore.InfoLevel
	if v := os.Getenv("ETCD_UI_LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}

	cfg := zap.NewProductionEncoderConfig()
	cfg.TimeKey = "ts"
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.LowercaseLevelEncoder

	encoder := zapcore.NewJSONEncoder(cfg)
	if os.Getenv("ETCD_UI_LOG_FORMAT") == "console" {
		encoder = zapcore.NewConsoleEncoder(cfg)
	}

	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), level)
	return zap.New(core, zap.AddCaller()).With(zap.String("service", service))
}
