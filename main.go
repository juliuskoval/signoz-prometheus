package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/SigNoz/signoz/pkg/query-service/model/metrics_explorer"
)

const (
	statusSuccess string = "success"
	statusError   string = "error"
)

func getQuery(w http.ResponseWriter, r *http.Request) {

	signozUrl := "http://localhost:8080/api/v1/query"

	req, err := http.NewRequest(r.Method, signozUrl, r.Body)
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

	client := &http.Client{}
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

func getSeries(w http.ResponseWriter, r *http.Request) {

	val := r.FormValue("start")
	fmt.Println(val)
	// Execute the request
	// response, _ := client.Do(req)
	// defer response.Body.Close()

	// // Decode the JSON response
	// var resp Resp
	// json.NewDecoder(response.Body).Decode(&resp)

	// jsonBytes, err := json.MarshalIndent(resp, "", "  ")
	// if err != nil {
	// 	fmt.Println("Failed to format JSON:", err)
	// 	return
	// }
	// fmt.Println(string(jsonBytes))

	// writeJSON(w, http.StatusOK, response)
}

func getQueryRange(w http.ResponseWriter, r *http.Request) {

	val := r.FormValue("start")
	fmt.Println(val)
	// Execute the request
	// response, _ := client.Do(req)
	// defer response.Body.Close()

	// // Decode the JSON response
	// var resp Resp
	// json.NewDecoder(response.Body).Decode(&resp)

	// jsonBytes, err := json.MarshalIndent(resp, "", "  ")
	// if err != nil {
	// 	fmt.Println("Failed to format JSON:", err)
	// 	return
	// }
	// fmt.Println(string(jsonBytes))

	// writeJSON(w, http.StatusOK, response)
}

func getLabels(w http.ResponseWriter, r *http.Request) {
	signozUrl := "http://localhost:8080/api/v1/metrics/filters/keys"

	req, err := http.NewRequest(r.Method, signozUrl, r.Body)
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

	// Read raw body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read backend response", http.StatusInternalServerError)
		return
	}

	// Decompress only if Content-Encoding == gzip
	var decompressed []byte
	if resp.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, "gzip decode failed", http.StatusBadGateway)
			return
		}
		defer reader.Close()
		decompressed, err = io.ReadAll(reader)
		if err != nil {
			http.Error(w, "failed to decompress gzip", http.StatusBadGateway)
			return
		}
	} else {
		decompressed = bodyBytes
	}

	// Unmarshal into generic response
	var signozApiResponse apiResponse
	if err := json.Unmarshal(decompressed, &signozApiResponse); err != nil {
		http.Error(w, "failed to parse backend JSON", http.StatusInternalServerError)
		return
	}

	// Marshal Data → specific type
	var labels metrics_explorer.FilterKeyResponse
	jsonBytes, _ := json.Marshal(signozApiResponse.Data)
	if err := json.Unmarshal(jsonBytes, &labels); err != nil {
		http.Error(w, "backend JSON format mismatch", http.StatusBadGateway)
		return
	}

	// Extract just the keys
	result := make([]string, 0, len(labels.AttributeKeys))
	for _, v := range labels.AttributeKeys {
		result = append(result, v.Key)
	}

	writeHttpResponse(w, result)
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

func getLabelValues(w http.ResponseWriter, r *http.Request) {

}

func main() {
	http.HandleFunc("/api/v1/query", getQuery)
	http.HandleFunc("/api/v1/query_range", getQueryRange)
	http.HandleFunc("/api/v1/series", getSeries)
	http.HandleFunc("/api/v1/labels", getLabels)
	http.HandleFunc("/api/v1/label/{label}/values", getLabelValues)

	log.Println("Starting server on :9092")
	if err := http.ListenAndServe(":9092", nil); err != nil {
		log.Fatalf("Could not start server: %s\n", err)
	}
}

type apiResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
}

type Labels []string
