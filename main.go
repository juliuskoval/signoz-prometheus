package main

import (
	"context"
	"os"
	"time"

	server "github.com/juliuskoval/signoz-prometheus/pkg/server"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// loggerName is the instrumentation scope reported on log records emitted
// through the OpenTelemetry zap bridge.
const loggerName = "github.com/juliuskoval/signoz-prometheus"

func main() {
	ctx := context.Background()

	// Start the OTel LoggerProvider before building the zap logger so the
	// bridge core has a provider to deliver records to. (The bridge also
	// resolves the global provider lazily, so even logs emitted here are
	// captured once the provider is installed.)
	shutdownLogging, err := server.InitLogging(ctx)
	if err != nil {
		panic("Failed to initialize OTel logging: " + err.Error())
	}
	defer shutdownTelemetry("logging", shutdownLogging)

	log := buildLogger()
	zap.ReplaceGlobals(log)
	defer log.Sync()

	shutdownTracing, err := server.InitTracing(ctx)
	if err != nil {
		zap.L().Fatal("Failed to initialize tracing", zap.Error(err))
	}
	defer shutdownTelemetry("tracing", shutdownTracing)

	s := server.BuildServer()

	s.RegisterRoutes()

	s.Start()
}

// shutdownTelemetry flushes and stops a telemetry provider with a bounded
// timeout so a stuck exporter can't hang process exit.
func shutdownTelemetry(name string, shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		zap.L().Error("Error shutting down telemetry", zap.String("component", name), zap.Error(err))
	}
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
	// NewProductionConfig writes every level to stderr, which VS Code's debug
	// console renders in red. Send logs to stdout so info/warn aren't colored
	// as errors (zap's own internal errors still go to ErrorOutputPaths/stderr).
	cfg.OutputPaths = []string{"stdout"}

	// Tee the stdout core with the OpenTelemetry bridge so every log line is
	// also exported (with the active trace/span IDs) to the configured OTLP
	// backend. The bridge core accepts all levels, so gate it with the same
	// LOG_LEVEL as the console core to keep the two sinks consistent.
	log, _ := cfg.Build(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		otelCore, err := zapcore.NewIncreaseLevelCore(otelzap.NewCore(loggerName), cfg.Level)
		if err != nil {
			// Fall back to the stdout-only core rather than dropping logging.
			os.Stderr.WriteString("Failed to attach OTel log bridge: " + err.Error() + "\n")
			return core
		}
		return zapcore.NewTee(core, otelCore)
	}))
	return log
}
