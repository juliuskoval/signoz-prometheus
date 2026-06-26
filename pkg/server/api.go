package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	qb "github.com/juliuskoval/signoz-prometheus/pkg/querybuilder"
	"github.com/juliuskoval/signoz-prometheus/pkg/util"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
	"go.uber.org/zap"
)

const (
	statusSuccess        = "success"
	nameField            = qb.PrometheusMetricName
	defaultQueryLookback = time.Hour
)

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Debug("Received an HTTP request", zap.String("url.full", r.RequestURI))
	apiUrl := s.signozBaseURL + "/api/v1/health"

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	util.CopyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	apiUrl := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiUrl += "?" + r.URL.RawQuery
	}

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	util.CopyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getQueryRange(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Info("Received an HTTP request", zap.String("url.full", r.RequestURI))

	if err := sanitizeQuery(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Error("Failed to parse request", zap.Error(err))
		return
	}

	apiUrl := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiUrl += "?" + r.URL.RawQuery
	}
	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	util.CopyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getLabels(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Info("Received an HTTP request", zap.String("url.full", r.RequestURI))

	query, err := qb.BuildGetLabelsQuery(r.URL.Query())
	if err != nil {
		log.Warn("Invalid request", zap.Error(err), zap.String("url.path", r.RequestURI))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.runClickHouseQuery(r, query)
	if err != nil {
		log.Error("ClickHouse query failed", zap.Error(err), zap.String("query", query))
		writeUpstreamError(w, err)
		return
	}

	result := make([]string, 0, len(rows))
	for _, row := range rows {
		name, ok := row.Data["label_name"].(string)
		if !ok || name == "" {
			continue
		}
		// Drop reserved pseudo-labels (e.g. __scope.name__, __temporality__);
		// keep __name__, which Prometheus clients expect from /labels.
		if name != nameField && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
			continue
		}
		result = append(result, name)
	}

	s.writeHttpResponse(w, result)
}

// runClickHouseQuery executes a raw ClickHouse SQL statement through SigNoz's
// query_range API (queryType clickhouse_sql, table panel) and returns the
// result rows.
func (s *Server) runClickHouseQuery(r *http.Request, query string) ([]QueryRow, error) {
	reqLogger(r).Info("Executing ClickHouse query", zap.String("query", query))

	reqBody := QueryRangeRequest{
		Step: 60,
		CompositeQuery: CompositeQuery{
			QueryType: "clickhouse_sql",
			PanelType: "table",
			ChQueries: map[string]ClickHouseQuery{
				"A": {Query: query},
			},
		},
	}

	end := time.Now().UnixMilli()
	if e, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		end = e * 1000
	}
	start := end - defaultQueryLookback.Milliseconds()
	if s, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		start = s * 1000
	}
	reqBody.Start = start
	reqBody.End = end

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to build query_range request: %w", err)
	}

	apiUrl := s.signozBaseURL + "/api/v4/query_range"
	resp, err := s.callSignozApi(r, http.MethodPost, apiUrl, data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := util.ReadRawBody(resp)
		return nil, &upstreamError{
			statusCode:  resp.StatusCode,
			contentType: resp.Header.Get("Content-Type"),
			body:        body,
		}
	}

	response, err := readBody(resp)
	if err != nil {
		return nil, err
	}

	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response data: %w", err)
	}

	var qr QueryRangeResponse
	if err := json.Unmarshal(jsonBytes, &qr); err != nil {
		return nil, fmt.Errorf("backend JSON format mismatch: %w", err)
	}

	return extractRows(qr), nil
}

// extractRows flattens a query_range result into rows. A clickhouse_sql result
// arrives as series (each string column carried as a label), while list/table
// panels carry the columns directly under Data; we read all three so the same
// query works regardless of how SigNoz formatted it.
func extractRows(qr QueryRangeResponse) []QueryRow {
	var rows []QueryRow
	for _, res := range qr.Result {
		rows = append(rows, res.List...)
		if res.Table != nil {
			rows = append(rows, res.Table.Rows...)
		}
		for _, sr := range res.Series {
			data := make(map[string]any, len(sr.Labels))
			for k, v := range sr.Labels {
				data[k] = v
			}
			rows = append(rows, QueryRow{Data: data})
		}
	}
	return rows
}

type upstreamError struct {
	statusCode  int
	contentType string
	body        []byte
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.statusCode, e.body)
}

// writeUpstreamError relays an error from runClickHouseQuery: a non-2xx SigNoz
// response is passed through with its original status and body; transport or
// decoding failures become 502 Bad Gateway.
func writeUpstreamError(w http.ResponseWriter, err error) {
	var ue *upstreamError
	if errors.As(err, &ue) {
		if ue.contentType != "" {
			w.Header().Set("Content-Type", ue.contentType)
		}
		w.WriteHeader(ue.statusCode)
		if _, werr := w.Write(ue.body); werr != nil {
			zap.L().Error("Failed to write upstream error response", zap.Error(werr))
		}
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func (s *Server) getLabelValues(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	vars := mux.Vars(r)
	label := vars["label"]
	label = model.UnescapeName(label, model.ValueEncodingEscaping)

	if label == nameField {
		s.getSeries(w, r)
		return
	}

	query, err := qb.BuildGetLabelValuesQuery(label, r.URL.Query())
	if err != nil {
		log.Warn("Invalid request", zap.Error(err), zap.String("url.path", r.RequestURI))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.runClickHouseQuery(r, query)
	if err != nil {
		log.Error("ClickHouse query failed", zap.Error(err), zap.String("query", query))
		writeUpstreamError(w, err)
		return
	}

	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if value, ok := row.Data["label_value"].(string); ok && value != "" {
			result = append(result, value)
		}
	}

	s.writeHttpResponse(w, result)
}

func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	query, err := qb.BuildGetSeriesQuery(r.URL.Query())
	if err != nil {
		log.Warn("Invalid request", zap.Error(err), zap.String("url.path", r.RequestURI))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.runClickHouseQuery(r, query)
	if err != nil {
		log.Error("ClickHouse query failed", zap.Error(err), zap.String("query", query))
		writeUpstreamError(w, err)
		return
	}

	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if name, ok := row.Data["metric_name"].(string); ok && name != "" {
			result = append(result, name)
		}
	}

	s.writeHttpResponse(w, result)
}

func (s *Server) getMetadata(w http.ResponseWriter, r *http.Request) {
	log := reqLogger(r)
	log.Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		http.Error(w, "metric query parameter is required", http.StatusBadRequest)
		return
	}

	apiUrl := fmt.Sprintf("%s/api/v1/metrics/%s/metadata", s.signozBaseURL, url.PathEscape(metric))
	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		log.Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metadata MetricDetailsDTO
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		log.Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}
	if err := json.Unmarshal(jsonBytes, &metadata); err != nil {
		log.Error("Failed to unmarshal response from SigNoz", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	data := make(map[string][]description)
	data[metric] = []description{
		{
			Type: metadata.Type, Help: metadata.Description, Unit: metadata.Unit,
		},
	}

	s.writeHttpResponse(w, data)
}

func (s *Server) handleFallback(w http.ResponseWriter, r *http.Request) {
	reqLogger(r).Warn("Unhandled route", zap.String("url.path", r.RequestURI))
	http.Error(w, "404: Not found", http.StatusNotFound)
}

// forwardUpstreamError returns true (after copying the response through to w)
// if resp is a non-2xx, so the caller should stop processing. Callers that
// parse the body otherwise mask SigNoz errors as 200 OK empty results.
func (s *Server) forwardUpstreamError(w http.ResponseWriter, resp *http.Response) bool {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false
	}
	util.CopyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		zap.L().Error("Error copying upstream error body", zap.Error(err))
	}
	return true
}

func (s *Server) writeHttpResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(&apiResponse{
		Status: statusSuccess,
		Data:   data,
	}); err != nil {
		zap.L().Error("Failed to write response", zap.Error(err))
	}
}

// sanitizeQuery strips two PromQL artifacts the upstream parser rejects:
// an `__ignore_usage__=""` selector and doubled quotes from over-encoded
// labels. Only applied when the query fails to parse as-is.
func sanitizeQuery(r *http.Request) error {
	q := r.URL.Query().Get("query")
	if _, err := parser.ParseExpr(q); err == nil {
		return nil
	}

	q = strings.ReplaceAll(q, "__ignore_usage__=\"\", ", "")
	q = strings.ReplaceAll(q, "\"\"", "\"")
	if _, err := parser.ParseExpr(q); err != nil {
		return fmt.Errorf("failed to parse query value %q", q)
	}

	query := r.URL.Query()
	query.Set("query", q)
	r.URL.RawQuery = query.Encode()
	return nil
}

func readBody(r *http.Response) (apiResponse, error) {
	var response apiResponse

	decompressed, err := util.ReadRawBody(r)
	if err != nil {
		return response, err
	}

	if err := json.Unmarshal(decompressed, &response); err != nil {
		return response, err
	}

	return response, nil
}

func (s *Server) callSignozApi(r *http.Request, method string, apiUrl string, body []byte) (*http.Response, error) {
	var resp *http.Response
	req, err := http.NewRequestWithContext(r.Context(), method, apiUrl, bytes.NewBuffer(body))
	if err != nil {
		return resp, err
	}

	util.CopyHeaders(req.Header, r.Header)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	reqLogger(r).Info("Sending an HTTP request", zap.String("url.full", apiUrl))
	return s.httpClient.Do(req)
}
