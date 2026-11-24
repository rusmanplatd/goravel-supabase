package functions_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/functions"
)

func TestFunctionsInvokeRegionAndAuth(t *testing.T) {
	var gotMethod, gotQuery string
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "Application/JSON; charset=utf-8")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := functions.New(srv.URL+"/functions/v1",
		functions.WithAPIKey("sb_publishable_test123"),
		functions.WithHTTPClient(srv.Client()),
	)

	res, err := c.Invoke(context.Background(), "hello", functions.InvokeOptions{
		Region: functions.RegionUsEast1,
		Body:   map[string]any{"name": "Ada"},
		Headers: map[string]string{
			"content-type": "application/vnd.api+json",
		},
	})
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotQuery != "forceFunctionRegion=us-east-1" {
		t.Fatalf("query = %q, want forceFunctionRegion=us-east-1", gotQuery)
	}
	if gotHeaders.Get("x-region") != "us-east-1" {
		t.Fatalf("x-region = %q", gotHeaders.Get("x-region"))
	}
	if gotHeaders.Get("apikey") != "sb_publishable_test123" {
		t.Fatalf("apikey = %q", gotHeaders.Get("apikey"))
	}
	if gotHeaders.Get("Authorization") != "" {
		t.Fatalf("expected omitted Bearer for sb_publishable key, got %q", gotHeaders.Get("Authorization"))
	}
	if vals := gotHeaders.Values("Content-Type"); len(vals) != 1 || vals[0] != "application/vnd.api+json" {
		t.Fatalf("Content-Type = %v, want single caller-supplied header", vals)
	}
	m, ok := res.Data.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("decoded data = %#v", res.Data)
	}

	// SetAuth should attach Bearer token and handle lowercase authorization case-insensitively.
	c2 := functions.New(srv.URL+"/functions/v1",
		functions.WithHTTPClient(srv.Client()),
		functions.WithHeaders(map[string]string{"authorization": "Bearer old"}),
	)
	c2.SetAuth("user-jwt")
	_, err = c2.Invoke(context.Background(), "hello", functions.InvokeOptions{Method: http.MethodDelete})
	if err != nil {
		t.Fatalf("Invoke after SetAuth error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if vals := gotHeaders.Values("Authorization"); len(vals) != 1 || vals[0] != "Bearer user-jwt" {
		t.Fatalf("Authorization = %v, want [Bearer user-jwt]", vals)
	}
	c2.SetAuth("")
	_, err = c2.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Invoke after SetAuth('') error: %v", err)
	}
	if gotHeaders.Get("Authorization") != "" {
		t.Fatalf("expected cleared Authorization after SetAuth(''), got %q", gotHeaders.Get("Authorization"))
	}
}

func TestFunctionsResponseTypesAndSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/functions/v1/sse":
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			flusher := w.(http.Flusher)
			_, _ = w.Write([]byte("data: first\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("data: second\n\n"))
			flusher.Flush()
		case "/functions/v1/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{1, 2, 3, 4})
		case "/functions/v1/multipart":
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("greeting", "hello")
			_ = mw.Close()
			w.Header().Set("Content-Type", mw.FormDataContentType())
			_, _ = w.Write(buf.Bytes())
		case "/functions/v1/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("plain response"))
		}
	}))
	defer srv.Close()

	c := functions.New(srv.URL+"/functions/v1", functions.WithHTTPClient(srv.Client()))

	// SSE unbuffered stream remains readable when InvokeOptions.Timeout is set
	res, err := c.Invoke(context.Background(), "sse", functions.InvokeOptions{Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("sse error: %v", err)
	}
	if res.Stream == nil {
		t.Fatal("expected non-nil Stream for SSE")
	}
	scanner := bufio.NewScanner(res.Stream)
	var lines []string
	for scanner.Scan() {
		if l := scanner.Text(); l != "" {
			lines = append(lines, l)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("SSE scanner error (timeout must not cancel live stream): %v", err)
	}
	res.Stream.Close()
	if len(lines) != 2 || lines[0] != "data: first" || lines[1] != "data: second" {
		t.Fatalf("SSE lines = %v", lines)
	}

	// Binary
	binRes, err := c.Invoke(context.Background(), "bin", functions.InvokeOptions{Body: []byte{9, 8}})
	if err != nil {
		t.Fatalf("bin error: %v", err)
	}
	if b, ok := binRes.Data.([]byte); !ok || len(b) != 4 {
		t.Fatalf("bin data = %#v", binRes.Data)
	}

	// Multipart
	mpRes, err := c.Invoke(context.Background(), "multipart", functions.InvokeOptions{
		Body: functions.MultipartBody{
			Fields: map[string]string{"k": "v"},
			Files:  []functions.MultipartFile{{FieldName: "f", FileName: "a.txt", Reader: strings.NewReader("abc")}},
		},
	})
	if err != nil {
		t.Fatalf("multipart error: %v", err)
	}
	if mpRes.Form == nil || mpRes.Form.Value["greeting"][0] != "hello" {
		t.Fatalf("multipart form = %#v", mpRes.Form)
	}

	// Text
	txtRes, err := c.Invoke(context.Background(), "text", functions.InvokeOptions{Body: "hi"})
	if err != nil {
		t.Fatalf("text error: %v", err)
	}
	if txtRes.Data != "plain response" {
		t.Fatalf("text data = %#v", txtRes.Data)
	}
}

func TestFunctionsErrorsAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/functions/v1/relay":
			w.Header().Set("x-relay-error", "true")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("relay failed"))
		case "/functions/v1/http500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		case "/functions/v1/slow":
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := functions.New(srv.URL+"/functions/v1", functions.WithHTTPClient(srv.Client()))

	_, err := c.Invoke(context.Background(), "relay")
	var relayErr *functions.FunctionsRelayError
	var baseErr *functions.FunctionsError
	if !errors.As(err, &relayErr) || !errors.As(err, &baseErr) {
		t.Fatalf("expected FunctionsRelayError & FunctionsError, got %T (%v)", err, err)
	}
	if _, jerr := json.Marshal(relayErr); jerr != nil {
		t.Fatalf("json.Marshal(relayErr) failed: %v", jerr)
	}

	_, err = c.Invoke(context.Background(), "http500")
	var httpErr *functions.FunctionsHttpError
	if !errors.As(err, &httpErr) || !errors.As(err, &baseErr) {
		t.Fatalf("expected FunctionsHttpError & FunctionsError, got %T (%v)", err, err)
	}
	if _, jerr := json.Marshal(httpErr); jerr != nil {
		t.Fatalf("json.Marshal(httpErr) failed: %v", jerr)
	}
	if string(httpErr.Body) != `{"error":"boom"}` {
		t.Fatalf("httpErr.Body = %q", string(httpErr.Body))
	}
	bodyRead, _ := io.ReadAll(httpErr.Response.Body)
	if string(bodyRead) != `{"error":"boom"}` {
		t.Fatalf("httpErr.Response.Body = %q", string(bodyRead))
	}

	_, err = c.Invoke(context.Background(), "slow", functions.InvokeOptions{Timeout: 40 * time.Millisecond})
	var fetchErr *functions.FunctionsFetchError
	if !errors.As(err, &fetchErr) || !errors.As(err, &baseErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected FunctionsFetchError wrapping DeadlineExceeded, got %T (%v)", err, err)
	}
	if _, jerr := json.Marshal(fetchErr); jerr != nil {
		t.Fatalf("json.Marshal(fetchErr) failed: %v", jerr)
	}
}
