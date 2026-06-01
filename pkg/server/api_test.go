package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestWriteUpstreamError(t *testing.T) {
	// A non-2xx SigNoz response is relayed verbatim (status, content-type, body).
	rec := httptest.NewRecorder()
	writeUpstreamError(rec, &upstreamError{
		statusCode:  http.StatusBadRequest,
		contentType: "application/json",
		body:        []byte(`{"error":"bad query"}`),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if rec.Body.String() != `{"error":"bad query"}` {
		t.Errorf("body = %q", rec.Body.String())
	}

	// A transport/decoding error (no usable upstream response) becomes 502.
	rec2 := httptest.NewRecorder()
	writeUpstreamError(rec2, errors.New("connection refused"))
	if rec2.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec2.Code)
	}
}

func TestExtractRows(t *testing.T) {
	// Rows may arrive under series, list, or table; all three are read.
	body := `{"result":[{"queryName":"A",
		"series":[{"labels":{"label_name":"service.name"}},{"labels":{"label_name":"__name__"}}],
		"list":[{"data":{"label_name":"from_list"}}],
		"table":{"rows":[{"data":{"label_name":"from_table"}}]}
	}]}`
	var qr QueryRangeResponse
	if err := json.Unmarshal([]byte(body), &qr); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, row := range extractRows(qr) {
		if v, ok := row.Data["label_name"].(string); ok {
			got[v] = true
		}
	}
	for _, want := range []string{"service.name", "__name__", "from_list", "from_table"} {
		if !got[want] {
			t.Errorf("missing row %q (got %v)", want, got)
		}
	}
}

// mockSigNoz returns a server whose query_range responds with a series result
// carrying the given label_name values.
func mockSigNoz(t *testing.T, key string, values ...string) *httptest.Server {
	t.Helper()
	series := make([]map[string]any, 0, len(values))
	for _, v := range values {
		series = append(series, map[string]any{"labels": map[string]string{key: v}})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"result": []map[string]any{{"queryName": "A", "series": series}}},
		})
	}))
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return resp.Data
}

func TestGetLabelsFiltersPseudoLabels(t *testing.T) {
	upstream := mockSigNoz(t, "label_name",
		"__name__", "__scope.name__", "__temporality__", "service.name", "job_name")
	defer upstream.Close()
	s := &Server{signozBaseURL: upstream.URL, httpClient: upstream.Client()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/labels", nil)
	rec := httptest.NewRecorder()
	s.getLabels(rec, req)

	got := decodeData(t, rec)
	want := map[string]bool{"__name__": true, "service.name": true, "job_name": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v (pseudo-labels should be dropped)", got, want)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected label %q — reserved pseudo-label not filtered", v)
		}
	}
}

func TestGetLabelsBadSelectorReturns400(t *testing.T) {
	// Unparseable match[] must 400 before any upstream call (httpClient is nil).
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet,
		`/api/v1/labels?match%5B%5D=%7Bservice.name%3D%22x%22%7D`, nil)
	rec := httptest.NewRecorder()
	s.getLabels(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestGetLabelValues(t *testing.T) {
	upstream := mockSigNoz(t, "label_value", "clickhouse", "redis")
	defer upstream.Close()
	s := &Server{signozBaseURL: upstream.URL, httpClient: upstream.Client()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/label/service.name/values", nil)
	req = mux.SetURLVars(req, map[string]string{"label": "service.name"})
	rec := httptest.NewRecorder()
	s.getLabelValues(rec, req)

	got := decodeData(t, rec)
	if len(got) != 2 || got[0] != "clickhouse" || got[1] != "redis" {
		t.Errorf("got %v, want [clickhouse redis]", got)
	}
}

func TestGetSeriesReturnsMetricNames(t *testing.T) {
	// getLabelValues for __name__ short-circuits to getSeries.
	upstream := mockSigNoz(t, "metric_name", "up", "http_requests_total")
	defer upstream.Close()
	s := &Server{signozBaseURL: upstream.URL, httpClient: upstream.Client()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/label/__name__/values", nil)
	req = mux.SetURLVars(req, map[string]string{"label": "__name__"})
	rec := httptest.NewRecorder()
	s.getLabelValues(rec, req)

	got := decodeData(t, rec)
	if len(got) != 2 || got[0] != "up" || got[1] != "http_requests_total" {
		t.Errorf("got %v, want [up http_requests_total]", got)
	}
}

func TestHandlerRelaysUpstreamStatus(t *testing.T) {
	// A 400 from SigNoz is passed through as 400, not masked as 502.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":"bad clickhouse sql"}`))
	}))
	defer upstream.Close()
	s := &Server{signozBaseURL: upstream.URL, httpClient: upstream.Client()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/labels", nil)
	rec := httptest.NewRecorder()
	s.getLabels(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (upstream status should pass through)", rec.Code)
	}
}
