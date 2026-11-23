package storage

import (
	"encoding/json"
	"fmt"
)

// StorageError is the base error for Supabase Storage operations.
type StorageError struct {
	Name    string
	Message string
	Err     error
}

func (e *StorageError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Name, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Name, e.Message)
}

func (e *StorageError) Unwrap() error { return e.Err }

// StorageApiError represents an HTTP error returned by the Supabase Storage API.
type StorageApiError struct {
	*StorageError
	StatusCode int
	Code       string
}

func (e *StorageApiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("StorageApiError (%s, status %d): %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("StorageApiError (status %d): %s", e.StatusCode, e.Message)
}

// StorageVectorsApiError represents an error returned by the Storage Vectors API.
type StorageVectorsApiError struct {
	*StorageError
	StatusCode int
	Code       string
}

func (e *StorageVectorsApiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("StorageVectorsApiError (%s, status %d): %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("StorageVectorsApiError (status %d): %s", e.StatusCode, e.Message)
}

// IcebergError represents an error returned by the Apache Iceberg REST Catalog API.
type IcebergError struct {
	*StorageError
	StatusCode int
	Type       string
	Code       int
}

func (e *IcebergError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("IcebergError (%s, status %d): %s", e.Type, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("IcebergError (status %d): %s", e.StatusCode, e.Message)
}

func decodeStorageApiError(body []byte, status int, statusText string) error {
	var raw struct {
		StatusCode any    `json:"statusCode"`
		Error      string `json:"error"`
		Message    string `json:"message"`
		Code       string `json:"code"`
	}
	msg := statusText
	code := ""
	if len(body) > 0 && json.Unmarshal(body, &raw) == nil {
		if raw.Message != "" {
			msg = raw.Message
		} else if raw.Error != "" {
			msg = raw.Error
		}
		if raw.Code != "" {
			code = raw.Code
		} else if raw.Error != "" {
			code = raw.Error
		}
	} else if len(body) > 0 {
		msg = string(body)
	}
	return &StorageApiError{
		StorageError: &StorageError{Name: "StorageApiError", Message: msg},
		StatusCode:   status,
		Code:         code,
	}
}

func decodeVectorsApiError(body []byte, status int, statusText string) error {
	var raw struct {
		Code    string `json:"code"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	msg := statusText
	code := ""
	if len(body) > 0 && json.Unmarshal(body, &raw) == nil {
		if raw.Message != "" {
			msg = raw.Message
		} else if raw.Error != "" {
			msg = raw.Error
		}
		if raw.Code != "" {
			code = raw.Code
		} else if raw.Error != "" {
			code = raw.Error
		}
	} else if len(body) > 0 {
		msg = string(body)
	}
	return &StorageVectorsApiError{
		StorageError: &StorageError{Name: "StorageVectorsApiError", Message: msg},
		StatusCode:   status,
		Code:         code,
	}
}

func decodeIcebergError(body []byte, status int, statusText string) error {
	var raw struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    int    `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := statusText
	errType := ""
	code := status
	if len(body) > 0 && json.Unmarshal(body, &raw) == nil {
		if raw.Error.Message != "" {
			msg = raw.Error.Message
			errType = raw.Error.Type
			if raw.Error.Code != 0 {
				code = raw.Error.Code
			}
		} else if raw.Message != "" {
			msg = raw.Message
		}
	} else if len(body) > 0 {
		msg = string(body)
	}
	return &IcebergError{
		StorageError: &StorageError{Name: "IcebergError", Message: msg},
		StatusCode:   status,
		Type:         errType,
		Code:         code,
	}
}
