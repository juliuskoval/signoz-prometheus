// Package util holds small, domain-agnostic helpers shared across the proxy.
package util

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
)

// hopByHopHeaders must not be forwarded by an intermediary (RFC 7230 §6.1).
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

// CopyHeaders copies src into dst, skipping hop-by-hop headers that an
// intermediary must not forward.
func CopyHeaders(dst, src http.Header) {
	for key, values := range src {
		if _, hop := hopByHopHeaders[http.CanonicalHeaderKey(key)]; hop {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// ReadRawBody reads a response body, transparently gunzipping it when the
// upstream set Content-Encoding: gzip.
func ReadRawBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.Header.Get("Content-Encoding") != "gzip" {
		return body, nil
	}

	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
