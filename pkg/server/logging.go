package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// reqLogger returns the global zap logger bound to the request context so the
// OpenTelemetry bridge stamps emitted records with the active trace and span
// IDs (the otelmux middleware put the server span in r.Context()). The context
// rides along as a zapcore.SkipType field: the bridge reads it from the field's
// Interface regardless of type, while the stdout JSON encoder skips it — so the
// console output is unchanged and only the OTLP log stream gains correlation.
func reqLogger(r *http.Request) *zap.Logger {
	return zap.L().With(zapcore.Field{Type: zapcore.SkipType, Interface: r.Context()})
}

// InitLogging configures a global OpenTelemetry LoggerProvider and returns a
// shutdown func that flushes any buffered records. The zap bridge installed in
// main wires application logs into this provider so they reach the same backend
// as the spans. To correlate a record with its span, pass the request context
// as a zap field, e.g. zap.Any("context", r.Context()); the bridge reads the
// trace/span IDs from it.
//
// The exporter is selectable via OTEL_LOGS_EXPORTER, mirroring OTEL_TRACES_EXPORTER:
//   - "otlp"    (default) export over OTLP; protocol from
//     OTEL_EXPORTER_OTLP_LOGS_PROTOCOL / OTEL_EXPORTER_OTLP_PROTOCOL. Every other
//     OTEL_EXPORTER_OTLP[_LOGS]_* setting is read by the exporter constructor.
//   - "console" write records to stdout (local debugging).
//   - "none"    disable OTLP log export; the global provider stays a no-op, so
//     the zap bridge becomes a cheap sink and only stdout logging remains.
//
// Call this before building the zap logger so records emitted during the rest
// of startup are captured.
func InitLogging(ctx context.Context) (func(context.Context) error, error) {
	exporter := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_LOGS_EXPORTER")))
	if exporter == "" {
		exporter = "otlp"
	}

	if exporter == "none" {
		zap.L().Info("OTel log export disabled (OTEL_LOGS_EXPORTER=none)")
		return func(context.Context) error { return nil }, nil
	}

	exp, err := buildLogExporter(ctx, exporter)
	if err != nil {
		return nil, err
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(newResource(ctx)),
	)
	global.SetLoggerProvider(lp)

	zap.L().Info("OTel logging initialized", zap.String("exporter", exporter))
	return lp.Shutdown, nil
}

func buildLogExporter(ctx context.Context, exporter string) (sdklog.Exporter, error) {
	switch exporter {
	case "otlp":
		return buildOTLPLogExporter(ctx)
	case "console", "stdout":
		return stdoutlog.New(stdoutlog.WithPrettyPrint())
	default:
		return nil, fmt.Errorf("unsupported OTEL_LOGS_EXPORTER %q (want \"otlp\", \"console\", or \"none\")", exporter)
	}
}

// buildOTLPLogExporter constructs the OTLP log exporter. As with traces, only
// the wire protocol is resolved here; the endpoint, headers, timeout,
// compression, and TLS settings are read from the OTEL_EXPORTER_OTLP[_LOGS]_*
// env vars by the constructor itself.
func buildOTLPLogExporter(ctx context.Context) (sdklog.Exporter, error) {
	protocol := resolveOTLPProtocol("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")
	switch protocol {
	case "grpc":
		return otlploggrpc.New(ctx)
	case "", "http/protobuf":
		return otlploghttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP protocol %q (want \"grpc\" or \"http/protobuf\")", protocol)
	}
}
