package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"go.uber.org/zap"
)

const (
	statusSuccess string = "success"
	nameField     string = "__name__"
)

// hop-by-hop headers must not be forwarded by an intermediary (RFC 7230 §6.1).
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if _, hop := hopByHopHeaders[http.CanonicalHeaderKey(key)]; hop {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

var v2 bool = true

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	zap.L().Debug("Received an HTTP request", zap.String("url.full", r.RequestURI))
	apiUrl := s.signozBaseURL + "/api/v1/health"

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		zap.L().Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	apiUrl := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiUrl += "?" + r.URL.RawQuery
	}

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		zap.L().Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getQueryRange(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))

	if err := sanitizeQuery(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		zap.L().Error("Failed to parse request", zap.Error(err))
		return
	}

	apiUrl := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiUrl += "?" + r.URL.RawQuery
	}
	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		zap.L().Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getLabels(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))

	params := url.Values{}
	params.Set("signal", "metrics")

	match := r.URL.Query().Get("match[]")
	match = strings.ReplaceAll(match, "\"\"", "\"")

	matcher, err := parser.ParseMetricSelector(match)
	if err != nil {
		zap.L().Warn("Failed to parse matcher", zap.Error(err), zap.String("url.path", r.RequestURI))
	}

	for _, v := range matcher {
		if v.Name == nameField && v.Type == labels.MatchEqual {
			params.Set("metricName", v.Value)
		} else if v.Name == nameField && v.Type == labels.MatchRegexp {
			metricName := strings.ReplaceAll(v.Value, ".*", "")
			metricName = strings.ReplaceAll(metricName, ".+", "")
			params.Set("metricName", metricName)
		}
	}

	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		params.Set("startUnixMilli", strconv.FormatInt(start*1000, 10))
	}

	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		params.Set("endUnixMilli", strconv.FormatInt(end*1000, 10))
	}

	apiUrl := s.signozBaseURL + "/api/v1/fields/keys?" + params.Encode()

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var keys fieldKeysResponse
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		zap.L().Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusInternalServerError)
		return
	}
	if err := json.Unmarshal(jsonBytes, &keys); err != nil {
		http.Error(w, "backend JSON format mismatch", http.StatusInternalServerError)
		return
	}

	result := make([]string, 0, len(keys.Keys))
	for _, v := range keys.Keys {
		if len(v) == 0 || v[0] == nil {
			continue
		}
		result = append(result, v[0].Name)
	}

	s.writeHttpResponse(w, result)
}

func (s *Server) getLabelValues(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	vars := mux.Vars(r)
	label := vars["label"]
	label = model.UnescapeName(label, model.ValueEncodingEscaping)

	if label == nameField {
		s.getSeries(w, r)
		return
	}

	params := url.Values{}
	params.Set("signal", "metrics")
	params.Set("name", label)

	match := r.URL.Query().Get("match[]")
	match = strings.ReplaceAll(match, "\"\"", "\"")

	if match != "" {
		matcher, err := parser.ParseMetricSelector(match)
		if err != nil {
			zap.L().Warn("Failed to parse matcher", zap.Error(err), zap.String("url", r.RequestURI))
		}

		for _, v := range matcher {
			if v.Type == labels.MatchRegexp {
				params.Set("searchText", strings.ReplaceAll(v.Value, ".*", ""))
			} else if v.Type == labels.MatchEqual && v.Name == nameField {
				params.Set("metricName", v.Value)
			}
		}
	}

	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		params.Set("startUnixMilli", strconv.FormatInt(start*1000, 10))
	}

	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		params.Set("endUnixMilli", strconv.FormatInt(end*1000, 10))
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if _, err := strconv.ParseInt(limitStr, 10, 64); err == nil {
			params.Set("limit", limitStr)
		} else {
			zap.L().Warn("Invalid limit parameter, ignoring", zap.String("limit", limitStr))
		}
	}

	apiUrl := s.signozBaseURL + "/api/v1/fields/values?" + params.Encode()

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var values fieldValuesResponse
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		zap.L().Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}
	if err := json.Unmarshal(jsonBytes, &values); err != nil {
		zap.L().Error("Failed to unmarshal response from SigNoz", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	if values.Values == nil {
		s.writeHttpResponse(w, []string{})
		return
	}

	s.writeHttpResponse(w, values.Values.StringValues)
}

func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	if v2 {
		s.getMetricsV2(w, r)
		return
	}

	apiUrl := s.signozBaseURL + "/api/v1/metrics"
	match := r.URL.Query().Get("match[]")

	req := SummaryListMetricsRequest{}
	req.Limit = 1000

	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		req.Start = start * 1000
	}

	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		req.End = end * 1000
	}

	if match != "" {
		matcher, err := parser.ParseMetricSelector(match)
		if err != nil {
			zap.L().Error("Failed to parse matcher", zap.Error(err), zap.String("url.path", r.RequestURI))
		}

		fs := FilterSet{}
		for _, v := range matcher {
			if v.Name == nameField {
				item := FilterItem{
					Key: AttributeKey{
						Key: "metric_name",
					},
				}
				switch v.Type {
				case labels.MatchEqual:
					item.Operator = FilterOperatorEqual
					item.Value = v.Value
				case labels.MatchNotEqual:
					item.Operator = FilterOperatorNotEqual
					item.Value = v.Value
				case labels.MatchRegexp:
					item.Operator = FilterOperatorContains
					item.Value = strings.ReplaceAll(v.Value, ".*", "")
				case labels.MatchNotRegexp:
					item.Operator = FilterOperatorNotContains
					item.Value = strings.ReplaceAll(v.Value, ".*", "")
				}
				fs.Items = append(fs.Items, item)
			}
		}
		req.Filters = fs
	}

	data, err := json.Marshal(req)
	if err != nil {
		zap.L().Error("Failed to marshal request", zap.Error(err))
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}

	resp, err := s.callSignozApi(r, http.MethodPost, apiUrl, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metrics SummaryListMetricsResponse
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		zap.L().Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}
	if err := json.Unmarshal(jsonBytes, &metrics); err != nil {
		zap.L().Error("Failed to unmarshal response from SigNoz", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	result := make([]string, 0, len(metrics.Metrics))
	for _, v := range metrics.Metrics {
		result = append(result, v.MetricName)
	}

	s.writeHttpResponse(w, result)
}

func (s *Server) getMetricsV2(w http.ResponseWriter, r *http.Request) {
	params := url.Values{}
	params.Set("limit", "1000")

	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		params.Set("start", strconv.FormatInt(start*1000, 10))
	}

	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		params.Set("end", strconv.FormatInt(end*1000, 10))
	}

	match := r.URL.Query().Get("match[]")
	if match != "" {
		matcher, err := parser.ParseMetricSelector(match)
		if err != nil {
			zap.L().Error("Failed to parse matcher", zap.Error(err), zap.String("url.path", r.RequestURI))
		}

		for _, v := range matcher {
			if v.Name == nameField {
				switch v.Type {
				case labels.MatchEqual:
					params.Set("searchText", v.Value)
				case labels.MatchRegexp:
					params.Set("searchText", strings.ReplaceAll(v.Value, ".*", ""))
				}
			}
		}
	}

	apiUrl := s.signozBaseURL + "/api/v2/metrics?" + params.Encode()

	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metrics ListMetricsResponse
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		zap.L().Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}
	if err := json.Unmarshal(jsonBytes, &metrics); err != nil {
		zap.L().Error("Failed to unmarshal response from SigNoz", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	result := make([]string, 0, len(metrics.Metrics))
	for _, v := range metrics.Metrics {
		result = append(result, v.MetricName)
	}

	s.writeHttpResponse(w, result)
}

func (s *Server) getMetadata(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		http.Error(w, "metric query parameter is required", http.StatusBadRequest)
		return
	}

	apiUrl := fmt.Sprintf("%s/api/v1/metrics/%s/metadata", s.signozBaseURL, url.PathEscape(metric))
	resp, err := s.callSignozApi(r, http.MethodGet, apiUrl, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiUrl), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if s.forwardUpstreamError(w, resp) {
		return
	}

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metadata MetricDetailsDTO
	jsonBytes, err := json.Marshal(response.Data)
	if err != nil {
		zap.L().Error("Failed to marshal response data", zap.Error(err))
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}
	if err := json.Unmarshal(jsonBytes, &metadata); err != nil {
		zap.L().Error("Failed to unmarshal response from SigNoz", zap.Error(err))
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
	zap.L().Warn("Unhandled route", zap.String("url.path", r.RequestURI))
	http.Error(w, "404: Not found", http.StatusNotFound)
}

// forwardUpstreamError returns true (after copying the response through to w)
// if resp is a non-2xx, so the caller should stop processing. Callers that
// parse the body otherwise mask SigNoz errors as 200 OK empty results.
func (s *Server) forwardUpstreamError(w http.ResponseWriter, resp *http.Response) bool {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false
	}
	copyHeaders(w.Header(), resp.Header)
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
		return errors.New("Failed to parse query value " + q)
	}

	query := r.URL.Query()
	query.Set("query", q)
	r.URL.RawQuery = query.Encode()
	return nil
}

func readBody(r *http.Response) (apiResponse, error) {
	var response apiResponse
	var decompressed []byte

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return response, err
	}

	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err != nil {
			return response, err
		}

		defer reader.Close()
		decompressed, err = io.ReadAll(reader)
		if err != nil {
			return response, err
		}
	} else {
		decompressed = bodyBytes
	}

	if err := json.Unmarshal(decompressed, &response); err != nil {
		return response, err
	}

	return response, nil
}

func (s *Server) callSignozApi(r *http.Request, method string, apiUrl string, body []byte) (*http.Response, error) {
	var resp *http.Response
	req, err := http.NewRequest(method, apiUrl, bytes.NewBuffer(body))
	if err != nil {
		return resp, err
	}

	copyHeaders(req.Header, r.Header)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	zap.L().Info("Sending an HTTP request", zap.String("url.full", apiUrl))
	return s.httpClient.Do(req)
}
