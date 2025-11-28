package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"go.uber.org/zap"

	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"github.com/SigNoz/signoz/pkg/types/telemetrytypes"
)

//TODO: table values - SigNoz bug?

const (
	statusSuccess string = "success"
	nameField     string = "__name__"
)

var (
	signozBaseUrl string = "https://signoz.ettech-uat.aws.dsarena.com"
	log           *zap.Logger
	httpClient    *http.Client
)

func main() {
	log, _ = zap.NewProduction()

	if endpoint := os.Getenv("SIGNOZ_URL"); endpoint != "" {
		if _, err := url.ParseRequestURI(endpoint); err != nil {
			log.Fatal("Invalid endpoint", zap.String("server.address", endpoint), zap.Error(err))
		}
		signozBaseUrl = endpoint
		log.Info("Setting SigNoz API endpoint", zap.String("server.address", endpoint))
	} else {
		log.Info("Using the default SigNoz endpoint", zap.String("server.address", signozBaseUrl))
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient = &http.Client{Transport: tr}

	r := mux.NewRouter()
	r.HandleFunc("/api/v1/query", getQuery)
	r.HandleFunc("/api/v1/query_range", getQueryRange)
	r.HandleFunc("/api/v1/labels", getLabels)
	r.HandleFunc("/api/v1/label/{label}/values", getLabelValues)

	log.Info("Starting server on :9092")
	if err := http.ListenAndServe(":9092", r); err != nil {
		log.Fatal("Could not start server", zap.Error(err))
	}
}

func getQuery(w http.ResponseWriter, r *http.Request) {
	log.Debug("Received HTTP request", zap.String("url.full", r.RequestURI))
	url := signozBaseUrl + r.RequestURI

	resp, err := callSignozApi(r, url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
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
		log.Error("Error copying response body", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
func getQueryRange(w http.ResponseWriter, r *http.Request) {
	log.Debug("Received HTTP request", zap.String("url.full", r.RequestURI))
	url := signozBaseUrl + r.RequestURI
	url = strings.ReplaceAll(url, "%22%22", "%22")

	resp, err := callSignozApi(r, url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occured while calling SigNoz API", zap.String("url.full", url), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	//TODO writeHttpResponse

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Error("Error copying response body", zap.Error(err))
	}
}

func getLabels(w http.ResponseWriter, r *http.Request) {
	log.Debug("Received HTTP request", zap.String("url.full", r.RequestURI))
	url := signozBaseUrl + "/api/v1/fields/keys?signal=metrics&"

	match := r.URL.Query().Get("match[]")
	if match == "" {
		match = r.URL.Query().Get("match%5B%5D")
	}
	match = strings.ReplaceAll(match, "\"\"", "\"")

	matcher, err := parser.ParseMetricSelector(match)

	for _, v := range matcher {
		if v.Name == nameField && v.Type == labels.MatchEqual {
			url = url + "&metricName=" + v.Value
		}
		if v.Name == nameField && v.Type == labels.MatchRegexp {
			url = url + "&searchText=" + strings.ReplaceAll(v.Value, ".*", "")
		}
	}

	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		url = url + "&startUnixMilli=" + strconv.FormatInt(start*1000, 10)
	}

	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		url = url + "&endUnixMilli=" + strconv.FormatInt(end*1000, 10)
	}

	resp, err := callSignozApi(r, url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occured while calling SigNoz API", zap.String("url.full", url), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	response, err := readBody(resp)

	var keys fieldKeysResponse
	jsonBytes, _ := json.Marshal(response.Data)
	if err := json.Unmarshal(jsonBytes, &keys); err != nil {
		http.Error(w, "backend JSON format mismatch", http.StatusInternalServerError)
		return
	}

	result := make([]string, 0, len(keys.Keys))
	for _, v := range keys.Keys {
		result = append(result, "\""+v[0].Name+"\"")
	}

	writeHttpResponse(w, result)
}

func getLabelValues(w http.ResponseWriter, r *http.Request) {
	log.Debug("Received HTTP request", zap.String("url.full", r.RequestURI))
	vars := mux.Vars(r)
	label := revertLabelName(vars["label"])
	url := signozBaseUrl

	match := r.URL.Query().Get("match[]")
	if match == "" {
		match = r.URL.Query().Get("match%5B%5D")
	}
	match = strings.ReplaceAll(match, "\"\"", "\"")

	matcher, err := parser.ParseMetricSelector(match)

	var metricName string
	var searchText string
	for _, v := range matcher {
		if v.Name == nameField && v.Type == labels.MatchEqual {
			metricName = v.Value
		} else if v.Name == nameField && v.Type == labels.MatchRegexp {
			searchText = v.Value
			searchText = strings.ReplaceAll(searchText, ".*", "")
		}
	}

	if label == nameField {
		url = signozBaseUrl + "/api/v3/autocomplete/aggregate_attributes?dataSource=metrics"
		if searchText != "" {
			url = url + "&searchText=" + searchText
		}
	} else {
		url = signozBaseUrl + "/api/v1/fields/values?signal=metrics&name=" + label
		if metricName != "" {
			url = url + "&metricName=" + metricName
		} else if searchText != "" {
			url = url + "&searchText=" + searchText
		}

		if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
			url = url + "&startUnixMilli=" + strconv.FormatInt(start*1000, 10)
		}

		if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
			url = url + "&endUnixMilli=" + strconv.FormatInt(end*1000, 10)
		}

	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		url = url + "&limit=" + limit
	}

	resp, err := callSignozApi(r, url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		log.Error("An error occured while calling SigNoz API", zap.String("url.full", url), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	response, err := readBody(resp)

	if label == nameField {
		var metrics v3.AggregateAttributeResponse
		jsonBytes, _ := json.Marshal(response.Data)
		if err := json.Unmarshal(jsonBytes, &metrics); err != nil {
			log.Error("Failed to unmarshal response from SigNoz", zap.Error(err))
			http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
			return
		}
		result := make([]string, 0, len(metrics.AttributeKeys))

		for _, v := range metrics.AttributeKeys {
			result = append(result, v.Key)
		}

		writeHttpResponse(w, result)
	} else {
		var values fieldValuesResponse
		jsonBytes, _ := json.Marshal(response.Data)
		if err := json.Unmarshal(jsonBytes, &values); err != nil {
			log.Error("Failed to unmarshal response from SigNoz", zap.Error(err))
			http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
			return
		}
		result := values.Values.StringValues

		writeHttpResponse(w, result)
	}
}

func writeHttpResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(&apiResponse{
		Status: statusSuccess,
		Data:   data,
	}); err != nil {
		log.Error("Failed to write response", zap.Error(err))
		http.Error(w, "failed to write response", http.StatusInternalServerError)
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

func callSignozApi(r *http.Request, url string) (*http.Response, error) {
	var resp *http.Response
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return resp, err
	}

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	log.Debug("Sending HTTP request to", zap.String("url.full", r.RequestURI))
	return httpClient.Do(req)
}

func revertLabelName(encoded string) string {
	const prefix = "U__"
	if !strings.HasPrefix(encoded, prefix) {
		return encoded
	}

	s := strings.TrimPrefix(encoded, prefix)

	re := regexp.MustCompile(`_([0-9a-fA-F]{2})_`)

	decoded := re.ReplaceAllStringFunc(s, func(m string) string {
		hexStr := m[1 : len(m)-1]
		b, err := hex.DecodeString(hexStr)
		if err != nil || len(b) == 0 {
			return m
		}
		return string(b[0])
	})

	return decoded
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
