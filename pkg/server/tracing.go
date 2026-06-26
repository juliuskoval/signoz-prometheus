package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.uber.org/zap"
)

// InitTracing configures a global OpenTelemetry TracerProvider and returns a
// shutdown func that flushes any buffered spans. Call the returned func before
// exit so the final batch is exported.
//
// The exporter is selectable via OTEL_TRACES_EXPORTER:
//   - "otlp"    (default) export over OTLP. See buildOTLPExporter for the full
//     set of OTEL_EXPORTER_OTLP_* env vars honored.
//   - "console" write spans to stdout (local debugging).
//   - "none"    disable tracing; returns a no-op shutdown.
//
// Service name and other resource attributes follow the standard
// OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES env vars, defaulting the
// service name to "signoz-prometheus".
func InitTracing(ctx context.Context) (func(context.Context) error, error) {
	exporter := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")))
	if exporter == "" {
		exporter = "otlp"
	}

	if exporter == "none" {
		zap.L().Info("Tracing disabled (OTEL_TRACES_EXPORTER=none)")
		return func(context.Context) error { return nil }, nil
	}

	exp, err := buildExporter(ctx, exporter)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName("signoz-prometheus")),
	)
	if err != nil {
		// A merge conflict (e.g. schema URL mismatch) shouldn't take down the
		// service; fall back to the exporter-only provider.
		zap.L().Warn("Failed to build full tracing resource, using defaults", zap.Error(err))
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	zap.L().Info("Tracing initialized", zap.String("exporter", exporter))
	return tp.Shutdown, nil
}

func buildExporter(ctx context.Context, exporter string) (sdktrace.SpanExporter, error) {
	switch exporter {
	case "otlp":
		return buildOTLPExporter(ctx)
	case "console", "stdout":
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER %q (want \"otlp\", \"console\", or \"none\")", exporter)
	}
}

// buildOTLPExporter constructs the OTLP trace exporter. Only the wire protocol
// is resolved here, because the protocol decides which constructor to call:
//
//	OTEL_EXPORTER_OTLP_TRACES_PROTOCOL (preferred) or OTEL_EXPORTER_OTLP_PROTOCOL
//	  "grpc"           -> OTLP/gRPC
//	  "http/protobuf"  -> OTLP/HTTP (default when unset)
//
// Every other setting on https://opentelemetry.io/docs/specs/otel/protocol/exporter/
// is read from the environment by the exporter constructor itself, in both the
// generic OTEL_EXPORTER_OTLP_* and the signal-specific OTEL_EXPORTER_OTLP_TRACES_*
// form (the latter takes precedence):
//
//	OTEL_EXPORTER_OTLP[_TRACES]_ENDPOINT             target URL
//	OTEL_EXPORTER_OTLP[_TRACES]_INSECURE             disable TLS (per-signal only honored for gRPC)
//	OTEL_EXPORTER_OTLP[_TRACES]_HEADERS              extra request headers (e.g. auth)
//	OTEL_EXPORTER_OTLP[_TRACES]_TIMEOUT              export timeout in ms
//	OTEL_EXPORTER_OTLP[_TRACES]_COMPRESSION          "gzip" or "none"
//	OTEL_EXPORTER_OTLP[_TRACES]_CERTIFICATE          server CA cert file
//	OTEL_EXPORTER_OTLP[_TRACES]_CLIENT_CERTIFICATE   client cert file (mTLS)
//	OTEL_EXPORTER_OTLP[_TRACES]_CLIENT_KEY           client key file (mTLS)
func buildOTLPExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	}

	switch protocol {
	case "grpc":
		return otlptracegrpc.New(ctx)
	case "", "http/protobuf":
		return otlptracehttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP protocol %q (want \"grpc\" or \"http/protobuf\")", protocol)
	}
}
