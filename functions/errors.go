package functions

import (
	"fmt"
	"net/http"
)

// FunctionsError is the base error for Supabase Edge Function invocations.
type FunctionsError struct {
	Name    string
	Message string
	Context any
	Err     error
}

func (e *FunctionsError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Name, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Name, e.Message)
}

func (e *FunctionsError) Unwrap() error {
	return e.Err
}

// FunctionsFetchError is returned when the network request to an Edge Function fails.
type FunctionsFetchError struct {
	*FunctionsError
}

func NewFunctionsFetchError(err error) *FunctionsFetchError {
	return &FunctionsFetchError{
		FunctionsError: &FunctionsError{
			Name:    "FunctionsFetchError",
			Message: "Failed to send a request to the Edge Function",
			Context: err,
			Err:     err,
		},
	}
}

// FunctionsRelayError is returned when the Supabase relay reports an error (x-relay-error: true).
type FunctionsRelayError struct {
	*FunctionsError
	Response *http.Response
	Body     []byte
}

func NewFunctionsRelayError(resp *http.Response, body []byte) *FunctionsRelayError {
	return &FunctionsRelayError{
		FunctionsError: &FunctionsError{
			Name:    "FunctionsRelayError",
			Message: "Relay Error invoking the Edge Function",
			Context: resp,
		},
		Response: resp,
		Body:     body,
	}
}

// FunctionsHttpError is returned when the Edge Function responds with a non-2xx status code.
type FunctionsHttpError struct {
	*FunctionsError
	Response *http.Response
	Body     []byte
}

func NewFunctionsHttpError(resp *http.Response, body []byte) *FunctionsHttpError {
	return &FunctionsHttpError{
		FunctionsError: &FunctionsError{
			Name:    "FunctionsHttpError",
			Message: "Edge Function returned a non-2xx status code",
			Context: resp,
		},
		Response: resp,
		Body:     body,
	}
}
