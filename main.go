package main

import (
	"os"

	server "github.com/juliuskoval/signoz-prometheus/pkg/server"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	log := buildLogger()
	zap.ReplaceGlobals(log)
	defer log.Sync()

	s := server.BuildServer()

	s.RegisterRoutes()

	s.Start()
}

func buildLogger() *zap.Logger {
	level := zapcore.InfoLevel
	if levelStr := os.Getenv("LOG_LEVEL"); levelStr != "" {
		if err := level.UnmarshalText([]byte(levelStr)); err != nil {
			os.Stderr.WriteString("Invalid LOG_LEVEL value \"" + levelStr + "\", defaulting to info\n")
		}
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(level)
	log, _ := cfg.Build()
	return log
}
