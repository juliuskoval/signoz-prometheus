package server

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.uber.org/zap"
)

// newResource builds the OpenTelemetry resource shared by the tracer and logger
// providers. Service name and other attributes follow the standard
// OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES env vars, defaulting the service
// name to "signoz-prometheus". A build error (e.g. schema URL mismatch) falls
// back to the default resource rather than taking the process down.
func newResource(ctx context.Context) *resource.Resource {
	res, err := resource.New(ctx,
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName("signoz-prometheus")),
	)
	if err != nil {
		zap.L().Warn("Failed to build full telemetry resource, using defaults", zap.Error(err))
		return resource.Default()
	}
	return res
}

// resolveOTLPProtocol returns the configured OTLP wire protocol for a signal.
// The protocol decides which exporter constructor to call, so it can't be read
// by the constructor itself like the other OTEL_EXPORTER_OTLP_* settings.
// A signal-specific override (e.g. OTEL_EXPORTER_OTLP_TRACES_PROTOCOL) takes
// precedence over the generic OTEL_EXPORTER_OTLP_PROTOCOL; "" means unset and
// the caller should default to http/protobuf.
func resolveOTLPProtocol(signalEnvVar string) string {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv(signalEnvVar)))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	}
	return protocol
}
