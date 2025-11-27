package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	v3 "github.com/SigNoz/signoz/pkg/query-service/model/v3"
	"github.com/SigNoz/signoz/pkg/types/telemetrytypes"
)

const (
	statusSuccess string = "success"
	statusError   string = "error"
	signozBaseUrl string = "http://localhost:8080"
	nameField     string = "__name__"
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/api/v1/query", getQuery)
	r.HandleFunc("/api/v1/query_range", getQueryRange)
	r.HandleFunc("/api/v1/labels", getLabels)
	r.HandleFunc("/api/v1/label/{label}/values", getLabelValues)

	log.Println("Starting server on :9092")
	if err := http.ListenAndServe(":9092", r); err != nil {
		log.Fatalf("Could not start server: %s\n", err)
	}
}

func getQuery(w http.ResponseWriter, r *http.Request) {

	url := signozBaseUrl + "/api/v1/query"

	req, err := http.NewRequest(r.Method, url, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.URL.RawQuery = r.URL.RawQuery

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
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
		log.Println("Error copying response body:", err)
	}
}
func getQueryRange(w http.ResponseWriter, r *http.Request) {

	url := signozBaseUrl + "/api/v1/query_range"

	req, err := http.NewRequest(r.Method, url, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.URL.RawQuery = strings.ReplaceAll(r.URL.RawQuery, "%22%22", "%22")

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
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
		log.Println("Error copying response body:", err)
	}
}

func getLabels(w http.ResponseWriter, r *http.Request) {
	url := signozBaseUrl + "/api/v1/fields/keys?signal=metrics&"

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
		}
		if v.Name == nameField && v.Type == labels.MatchRegexp {
			searchText = v.Value //TODO parse
			searchText = strings.ReplaceAll(searchText, ".*", "")
		}
	}

	start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
	if err == nil {
		url = url + "start=" + strconv.FormatInt(start*1000, 10)
	}

	end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
	if err == nil {
		url = url + "end=" + strconv.FormatInt(end*1000, 10)
	}
	if metricName != "" {
		url = url + "&metricName=" + metricName
	}
	if searchText != "" {
		url = url + "&searchText=" + searchText
	}

	req, err := http.NewRequest(r.Method, url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Forward headers
	for k, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	response, err := readBody(resp)

	// Marshal Data → specific type
	var keys fieldKeysResponse
	jsonBytes, _ := json.Marshal(response.Data)
	if err := json.Unmarshal(jsonBytes, &keys); err != nil {
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	// Extract just the keys
	result := make([]string, 0, len(keys.Keys))
	for _, v := range keys.Keys {
		result = append(result, "\""+v[0].Name+"\"")
	}

	writeHttpResponse(w, result)
}

func getLabelValues(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	label := revertLabelName(vars["label"])
	url := signozBaseUrl

	match := r.URL.Query().Get("match[]")
	if match == "" {
		match = r.URL.Query().Get("match%5B%5D")
	}

	matcher, err := parser.ParseMetricSelector(match)

	var metricName string
	var searchText string
	for _, v := range matcher {
		if v.Name == nameField && v.Type == labels.MatchEqual {
			metricName = v.Value
		}
		if v.Name == nameField && v.Type == labels.MatchRegexp {
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

	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		url = url + "&limit=" + limit
	}
	if start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64); err == nil {
		start = start * 1000
		url = url + "&start=" + strconv.FormatInt(start, 10)
	}
	if end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64); err == nil {
		end = end * 1000
		url = url + "&end=" + strconv.FormatInt(end, 10)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{Transport: tr}

	resp, err := client.Do(req)
	fmt.Print(req.URL.Host + req.URL.Path + req.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	response, err := readBody(resp)

	if label == nameField {
		var metrics v3.AggregateAttributeResponse
		jsonBytes, _ := json.Marshal(response.Data)
		if err := json.Unmarshal(jsonBytes, &metrics); err != nil {
			http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
			return //TODO
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
			http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
			return //TODO
		}
		result := values.Values.StringValues

		writeHttpResponse(w, result)
	}
}

func writeHttpResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Encode ONCE
	if err := json.NewEncoder(w).Encode(&apiResponse{
		Status: statusSuccess,
		Data:   data,
	}); err != nil {
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

func revertLabelName(encoded string) string {
	const prefix = "U__"
	if !strings.HasPrefix(encoded, prefix) {
		// Not an encoded label
		return encoded
	}

	// Strip U__ prefix
	s := strings.TrimPrefix(encoded, prefix)

	// Pattern matches: _XX_  (where XX is hex)
	re := regexp.MustCompile(`_([0-9a-fA-F]{2})_`)

	// Replace each matched sequence with its decoded byte
	decoded := re.ReplaceAllStringFunc(s, func(m string) string {
		hexStr := m[1 : len(m)-1] // strip leading and trailing underscores
		b, err := hex.DecodeString(hexStr)
		if err != nil || len(b) == 0 {
			return m // fallback: return original
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

type Labels []string
type Series []string
type Values []string
