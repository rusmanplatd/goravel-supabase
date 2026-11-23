package auth

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrUnsupported is returned for browser-DOM-only operations when no server callback is supplied.
	ErrUnsupported = errors.New("auth: operation requires browser DOM APIs; use the two-step HTTP API or supply a ceremony callback")
	// ErrSessionMissing is returned when an operation requires an active session.
	ErrSessionMissing = errors.New("auth: session missing")
	// ErrAccessTokenMode is returned when built-in Auth methods are called while third-party AccessToken mode is active.
	ErrAccessTokenMode = errors.New("auth: built-in Auth client is disabled when a custom AccessToken provider is configured")
)

// AuthError is the base error for GoTrue authentication failures.
type AuthError struct {
	Name    string
	Message string
	Status  int
	Code    string
	Err     error
}

func (e *AuthError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s, status %d): %s", e.Name, e.Code, e.Status, e.Message)
	}
	if e.Status != 0 {
		return fmt.Sprintf("%s (status %d): %s", e.Name, e.Status, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Name, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Name, e.Message)
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

// AuthApiError represents an HTTP error returned by the GoTrue server.
type AuthApiError struct {
	*AuthError
}

func decodeAuthError(body []byte, status int, statusText string) error {
	var raw struct {
		ErrorCode        string `json:"error_code"`
		Code             any    `json:"code"`
		Msg              string `json:"msg"`
		Message          string `json:"message"`
		ErrorDescription string `json:"error_description"`
		Error            string `json:"error"`
	}
	msg := statusText
	code := ""
	if len(body) > 0 && json.Unmarshal(body, &raw) == nil {
		switch {
		case raw.Msg != "":
			msg = raw.Msg
		case raw.Message != "":
			msg = raw.Message
		case raw.ErrorDescription != "":
			msg = raw.ErrorDescription
		case raw.Error != "":
			msg = raw.Error
		}
		if raw.ErrorCode != "" {
			code = raw.ErrorCode
		} else if s, ok := raw.Code.(string); ok {
			code = s
		}
	} else if len(body) > 0 {
		msg = string(body)
	}
	return &AuthApiError{
		AuthError: &AuthError{
			Name:    "AuthApiError",
			Message: msg,
			Status:  status,
			Code:    code,
		},
	}
}
