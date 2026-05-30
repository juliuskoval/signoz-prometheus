package util

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

func TestCopyHeadersSkipsHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer token")
	src.Set("X-Custom", "keep")
	src.Set("Connection", "keep-alive") // hop-by-hop
	src.Set("Transfer-Encoding", "chunked") // hop-by-hop

	dst := http.Header{}
	CopyHeaders(dst, src)

	if got := dst.Get("Authorization"); got != "Bearer token" {
		t.Errorf("Authorization = %q, want forwarded", got)
	}
	if got := dst.Get("X-Custom"); got != "keep" {
		t.Errorf("X-Custom = %q, want forwarded", got)
	}
	if got := dst.Get("Connection"); got != "" {
		t.Errorf("Connection = %q, want dropped (hop-by-hop)", got)
	}
	if got := dst.Get("Transfer-Encoding"); got != "" {
		t.Errorf("Transfer-Encoding = %q, want dropped (hop-by-hop)", got)
	}
}

func TestReadRawBodyPlain(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader([]byte("hello"))),
	}
	got, err := ReadRawBody(resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestReadRawBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte("gzipped payload")); err != nil {
		t.Fatal(err)
	}
	gw.Close()

	h := http.Header{}
	h.Set("Content-Encoding", "gzip")
	resp := &http.Response{
		Header: h,
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	got, err := ReadRawBody(resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "gzipped payload" {
		t.Errorf("got %q, want 'gzipped payload'", got)
	}
}
