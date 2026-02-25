package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/SigNoz/signoz/pkg/query-service/model/metrics_explorer"
	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"github.com/SigNoz/signoz/pkg/types/telemetrytypes"
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

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))
	apiURL := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiURL += "?" + r.URL.RawQuery
	}

	resp, err := s.callSignozApi(r, http.MethodGet, apiURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiURL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		zap.L().Error("Error copying response body", zap.Error(err))
	}
}

func (s *Server) getQueryRange(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("Received an HTTP request", zap.String("url.full", r.RequestURI))

	apiURL := s.signozBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		apiURL += "?" + r.URL.RawQuery
	}
	resp, err := s.callSignozApi(r, http.MethodGet, apiURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiURL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

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

	apiURL := s.signozBaseURL + "/api/v1/fields/keys?" + params.Encode()

	resp, err := s.callSignozApi(r, http.MethodGet, apiURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiURL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

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

	apiURL := s.signozBaseURL + "/api/v1/fields/values?" + params.Encode()

	resp, err := s.callSignozApi(r, http.MethodGet, apiURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiURL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

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
	apiURL := s.signozBaseURL + "/api/v1/metrics"
	match := r.URL.Query().Get("match[]")

	req := metrics_explorer.SummaryListMetricsRequest{}
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

		fs := v3.FilterSet{}
		for _, v := range matcher {
			if v.Name == nameField {
				item := v3.FilterItem{
					Key: v3.AttributeKey{
						Key: "metric_name",
					},
				}
				switch v.Type {
				case labels.MatchEqual:
					item.Operator = v3.FilterOperatorEqual
					item.Value = v.Value
				case labels.MatchNotEqual:
					item.Operator = v3.FilterOperatorNotEqual
					item.Value = v.Value
				case labels.MatchRegexp:
					item.Operator = v3.FilterOperatorContains
					item.Value = strings.ReplaceAll(v.Value, ".*", "")
				case labels.MatchNotRegexp:
					item.Operator = v3.FilterOperatorNotContains
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
		return
	}

	resp, err := s.callSignozApi(r, http.MethodPost, apiURL, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		zap.L().Error("An error occurred while calling SigNoz API", zap.String("url.full", apiURL), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	response, err := readBody(resp)
	if err != nil {
		zap.L().Error("Failed to read response from SigNoz API", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var metrics metrics_explorer.SummaryListMetricsResponse
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

func (s *Server) handleFallback(w http.ResponseWriter, r *http.Request) {
	zap.L().Warn("Unhandled route", zap.String("url.path", r.RequestURI))
	http.Error(w, "No handler defined for the route", http.StatusNotImplemented)
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

func (s *Server) callSignozApi(r *http.Request, method string, apiURL string, body []byte) (*http.Response, error) {
	var resp *http.Response
	req, err := http.NewRequest(method, apiURL, bytes.NewBuffer(body))
	if err != nil {
		return resp, err
	}

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	zap.L().Info("Sending an HTTP request", zap.String("url.full", apiURL))
	return s.httpClient.Do(req)
}

type apiResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
}

type fieldKeysResponse struct {
	Keys     map[string][]*telemetrytypes.TelemetryFieldKey `json:"keys"`
	Complete bool                                           `json:"complete"`
}

type fieldValuesResponse struct {
	Values   *telemetrytypes.TelemetryFieldValues `json:"values"`
	Complete bool                                 `json:"complete"`
}
