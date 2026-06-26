package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// recordExporter is an in-memory sdklog.Exporter that captures emitted records.
type recordExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, records...)
	return nil
}
func (e *recordExporter) Shutdown(context.Context) error   { return nil }
func (e *recordExporter) ForceFlush(context.Context) error { return nil }

// TestReqLoggerCorrelatesAndKeepsStdoutClean verifies that a log emitted via
// reqLogger (1) carries the active trace/span IDs on the exported OTel record,
// and (2) does not leak the carried context as a field on the stdout JSON sink.
func TestReqLoggerCorrelatesAndKeepsStdoutClean(t *testing.T) {
	exp := &recordExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })
	global.SetLoggerProvider(lp)

	// Build the same tee used in production: a JSON console core (to a buffer)
	// plus the OTel bridge core.
	var buf bytes.Buffer
	consoleCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&buf),
		zapcore.InfoLevel,
	)
	logger := zap.New(zapcore.NewTee(consoleCore, otelzap.NewCore("test-scope")))
	undo := zap.ReplaceGlobals(logger)
	t.Cleanup(undo)

	// A request whose context carries a known span.
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	req := httptest.NewRequest("GET", "/api/v1/labels", nil).
		WithContext(trace.ContextWithSpanContext(context.Background(), sc))

	reqLogger(req).Info("Received an HTTP request", zap.String("url.path", req.RequestURI))

	// (1) The exported record is correlated with the span.
	if len(exp.records) != 1 {
		t.Fatalf("expected 1 exported record, got %d", len(exp.records))
	}
	rec := exp.records[0]
	if rec.TraceID() != tid {
		t.Errorf("TraceID = %s, want %s", rec.TraceID(), tid)
	}
	if rec.SpanID() != sid {
		t.Errorf("SpanID = %s, want %s", rec.SpanID(), sid)
	}

	// (2) The stdout JSON line carries the message but not the context field.
	stdout := buf.String()
	if !strings.Contains(stdout, "Received an HTTP request") {
		t.Errorf("stdout missing log message: %s", stdout)
	}
	if strings.Contains(stdout, "context") {
		t.Errorf("stdout leaked the carried context field: %s", stdout)
	}
}
