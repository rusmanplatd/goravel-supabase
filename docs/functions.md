# Edge Functions (`functions`)

The `functions` package invokes Supabase Edge Functions with support for JSON, raw bytes, `io.Reader` streams, `multipart/form-data`, Server-Sent Events (`text/event-stream`), regional routing, and typed error handling.

- **Package:** `github.com/rusmanplatd/goravel-supabase/functions`
- **Access via Client:** `sb.Functions` (pure-Go) or `facades.Supabase().Functions()` (Goravel)

---

## 1. Full Standalone Example (JSON, Custom Headers, Region, Method & Error Handling)

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/functions"
)

type OrderResponse struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

func main() {
	sb, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_publishable_...",
		client.WithFunctionsRegion(functions.RegionUsEast1),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	res, err := sb.Functions.Invoke(ctx, "process-order", functions.InvokeOptions{
		Method:  "POST",
		Region:  functions.RegionEuWest1,
		Timeout: 10 * time.Second,
		Headers: map[string]string{
			"X-Idempotency-Key": "order-98765",
		},
		Body: map[string]any{
			"item_id":  "sku_123",
			"quantity": 2,
		},
	})
	if err != nil {
		var httpErr *functions.FunctionsHttpError
		var relayErr *functions.FunctionsRelayError
		var fetchErr *functions.FunctionsFetchError

		switch {
		case errors.As(err, &httpErr):
			log.Fatalf("Function HTTP %d: %s", httpErr.Status, string(httpErr.Body))
		case errors.As(err, &relayErr):
			log.Fatalf("Relay error %d: %s", relayErr.Status, string(relayErr.Body))
		case errors.As(err, &fetchErr):
			log.Fatalf("Fetch error: %v", fetchErr.Err)
		default:
			log.Fatal(err)
		}
	}

	var order OrderResponse
	if err := res.DecodeJSON(&order); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Order %s status: %s\n", order.OrderID, order.Status)
}
```

Available region constants: `RegionAny`, `RegionApNortheast1`, `RegionApNortheast2`, `RegionApSouth1`, `RegionApSoutheast1`, `RegionApSoutheast2`, `RegionCaCentral1`, `RegionEuCentral1`, `RegionEuWest1`, `RegionEuWest2`, `RegionEuWest3`, `RegionSaEast1`, `RegionUsEast1`, `RegionUsWest1`, `RegionUsWest2`.

---

## 2. Goravel Controller Example

```go
package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/functions"
)

type FunctionController struct{}

func (c *FunctionController) Hello(ctx http.Context) http.Response {
	jwt := ctx.Request().Header("Authorization")
	sb := facades.Supabase().WithToken(jwt)

	res, err := sb.Functions().Invoke(ctx.Context(), "hello-world", functions.InvokeOptions{
		Region: functions.RegionApSoutheast1,
		Body:   map[string]any{"name": ctx.Request().Input("name", "Goravel")},
	})
	if err != nil {
		return ctx.Response().Json(502, http.Json{"error": err.Error()})
	}

	return ctx.Response().Success().Json(res.Data)
}
```

---

## 3. Multipart Form-Data Upload (`MultipartBody`)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/functions"
)

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_publishable_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	file, err := os.Open("invoice.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	res, err := sb.Functions.Invoke(context.Background(), "parse-pdf", functions.InvokeOptions{
		Body: functions.MultipartBody{
			Fields: map[string]string{
				"category": "invoice",
				"currency": "USD",
			},
			Files: []functions.MultipartFile{
				{
					FieldName:   "file",
					FileName:    "invoice.pdf",
					ContentType: "application/pdf",
					Reader:      file,
				},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Parsed result:", res.Data)
}
```

---

## 4. Streaming Responses (`text/event-stream` & Binary)

When an Edge Function returns `text/event-stream`, `res.Stream` is an `io.ReadCloser` that the caller must close:

```go
res, err := sb.Functions.Invoke(ctx, "stream-ai", functions.InvokeOptions{
	Body: map[string]any{"prompt": "Summarize Goravel"},
})
if err != nil {
	log.Fatal(err)
}

if res.Stream != nil {
	defer res.Stream.Close()
	scanner := bufio.NewScanner(res.Stream)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
```

For binary responses (`application/octet-stream` or `application/pdf`), `res.Data` is `[]byte` (and `res.RawBody` always contains the raw response bytes for non-streaming responses).
