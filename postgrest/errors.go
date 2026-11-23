package postgrest

import (
	"encoding/json"
	"fmt"
)

// PostgrestError represents an error returned by PostgREST or a client-side request failure.
type PostgrestError struct {
	Message    string `json:"message"`
	Details    string `json:"details"`
	Hint       string `json:"hint"`
	Code       string `json:"code"`
	Status     int    `json:"-"`
	StatusText string `json:"-"`
}

func (e *PostgrestError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("postgrest (%s): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("postgrest: %s", e.Message)
}

func decodePostgrestError(body []byte, status int, statusText string) *PostgrestError {
	var pe PostgrestError
	if len(body) > 0 && json.Unmarshal(body, &pe) == nil && (pe.Message != "" || pe.Code != "" || pe.Details != "") {
		pe.Status = status
		pe.StatusText = statusText
		return &pe
	}
	msg := string(body)
	if msg == "" {
		msg = statusText
	}
	return &PostgrestError{
		Message:    msg,
		Status:     status,
		StatusText: statusText,
	}
}
