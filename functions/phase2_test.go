package functions_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/internal/transport"
)

func TestFunctionsPhase2TraceMultipartMethodsRegionAndDecodeJSON(t *testing.T) {
	var (
		gotMethod, gotQuery, gotRegion, gotTraceparent, gotBody string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotRegion = r.Header.Get("x-region")
		gotTraceparent = r.Header.Get("traceparent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		switch r.URL.Path {
		case "/functions/v1/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4"))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"count":7}`))
		}
	}))
	defer srv.Close()

	c := functions.New(srv.URL+"/functions/v1",
		functions.WithHTTPClient(srv.Client()),
		functions.WithRegion(functions.RegionEuWest1),
		functions.WithTracePropagation(transport.TracePropagationOptions{
			Enabled: true,
			Targets: []string{srv.URL},
		}),
	)

	// Trace propagation + default WithRegion + MultipartFile custom ContentType
	tctx := transport.WithTraceContext(context.Background(), transport.TraceContext{
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})
	res, err := c.Invoke(tctx, "upload", functions.InvokeOptions{
		Method: http.MethodPut,
		Body: functions.MultipartBody{
			Files: []functions.MultipartFile{{
				FieldName:   "img",
				FileName:    "pic.png",
				ContentType: "image/png",
				Reader:      strings.NewReader("PNGDATA"),
			}},
		},
	})
	if err != nil {
		t.Fatalf("Invoke upload error: %v", err)
	}
	if gotMethod != http.MethodPut || gotRegion != "eu-west-1" || gotTraceparent == "" {
		t.Fatalf("method=%q region=%q traceparent=%q", gotMethod, gotRegion, gotTraceparent)
	}
	if !strings.Contains(gotBody, "Content-Type: image/png") {
		t.Fatalf("multipart body missing custom Content-Type: %s", gotBody)
	}
	var decoded struct {
		Count int `json:"count"`
	}
	if err := res.DecodeJSON(&decoded); err != nil || decoded.Count != 7 {
		t.Fatalf("DecodeJSON = %+v, %v", decoded, err)
	}

	// RegionAny reverts WithRegion + GET/PATCH + io.Reader body
	if _, err := c.Invoke(context.Background(), "fn?a=1", functions.InvokeOptions{
		Method: http.MethodPatch,
		Region: functions.RegionAny,
		Body:   strings.NewReader("stream-body"),
	}); err != nil {
		t.Fatalf("PATCH RegionAny error: %v", err)
	}
	if gotMethod != http.MethodPatch || gotRegion != "" || strings.Contains(gotQuery, "forceFunctionRegion") || gotBody != "stream-body" {
		t.Fatalf("RegionAny got method=%q region=%q query=%q body=%q", gotMethod, gotRegion, gotQuery, gotBody)
	}

	// application/pdf -> []byte
	pdfRes, err := c.Invoke(context.Background(), "pdf", functions.InvokeOptions{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("PDF invoke error: %v", err)
	}
	if b, ok := pdfRes.Data.([]byte); !ok || string(b) != "%PDF-1.4" {
		t.Fatalf("PDF data = %#v", pdfRes.Data)
	}

	// context.WithCancel aborts request
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Invoke(cctx, "fn"); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
