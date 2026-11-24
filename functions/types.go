package functions

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// FunctionRegion identifies a Supabase Edge Functions execution region.
type FunctionRegion string

const (
	RegionAny          FunctionRegion = "any"
	RegionApNortheast1 FunctionRegion = "ap-northeast-1"
	RegionApNortheast2 FunctionRegion = "ap-northeast-2"
	RegionApSouth1     FunctionRegion = "ap-south-1"
	RegionApSoutheast1 FunctionRegion = "ap-southeast-1"
	RegionApSoutheast2 FunctionRegion = "ap-southeast-2"
	RegionCaCentral1   FunctionRegion = "ca-central-1"
	RegionEuCentral1   FunctionRegion = "eu-central-1"
	RegionEuWest1      FunctionRegion = "eu-west-1"
	RegionEuWest2      FunctionRegion = "eu-west-2"
	RegionEuWest3      FunctionRegion = "eu-west-3"
	RegionSaEast1      FunctionRegion = "sa-east-1"
	RegionUsEast1      FunctionRegion = "us-east-1"
	RegionUsWest1      FunctionRegion = "us-west-1"
	RegionUsWest2      FunctionRegion = "us-west-2"
)

// MultipartFile represents a file part in a MultipartBody payload.
type MultipartFile struct {
	FieldName   string
	FileName    string
	ContentType string
	Reader      io.Reader
}

// MultipartBody represents a multipart/form-data request payload.
type MultipartBody struct {
	Fields map[string]string
	Files  []MultipartFile
}

// InvokeOptions configures a single Edge Function invocation.
type InvokeOptions struct {
	Headers map[string]string
	Method  string
	Region  FunctionRegion
	Body    any
	Timeout time.Duration
}

// InvokeResponse holds the decoded response from an Edge Function invocation.
type InvokeResponse struct {
	// Data holds the decoded payload:
	//   - application/json: decoded JSON value (map[string]any, []any, etc.)
	//   - application/octet-stream or application/pdf: []byte
	//   - multipart/form-data: *multipart.Form
	//   - text/event-stream: io.ReadCloser (same as Stream; caller must close)
	//   - default/text: string
	Data     any
	RawBody  []byte
	Stream   io.ReadCloser
	Form     *multipart.Form
	Response *http.Response
}

// DecodeJSON unmarshals the raw JSON response body into dst.
func (r *InvokeResponse) DecodeJSON(dst any) error {
	return json.Unmarshal(r.RawBody, dst)
}
