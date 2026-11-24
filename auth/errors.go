package auth

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrorCode represents typed GoTrue error codes.
type ErrorCode string

const (
	ErrorCodeUnexpectedFailure      ErrorCode = "unexpected_failure"
	ErrorCodeValidationFailed       ErrorCode = "validation_failed"
	ErrorCodeBadJSON                ErrorCode = "bad_json"
	ErrorCodeBadJWT                 ErrorCode = "bad_jwt"
	ErrorCodeInvalidCredentials     ErrorCode = "invalid_credentials"
	ErrorCodeSessionNotFound        ErrorCode = "session_not_found"
	ErrorCodeSessionExpired         ErrorCode = "session_expired"
	ErrorCodeRefreshTokenNotFound   ErrorCode = "refresh_token_not_found"
	ErrorCodeRefreshTokenUsed       ErrorCode = "refresh_token_already_used"
	ErrorCodeUserNotFound           ErrorCode = "user_not_found"
	ErrorCodeUserAlreadyExists      ErrorCode = "user_already_exists"
	ErrorCodeEmailExists            ErrorCode = "email_exists"
	ErrorCodePhoneExists            ErrorCode = "phone_exists"
	ErrorCodeEmailNotConfirmed      ErrorCode = "email_not_confirmed"
	ErrorCodePhoneNotConfirmed      ErrorCode = "phone_not_confirmed"
	ErrorCodeWeakPassword           ErrorCode = "weak_password"
	ErrorCodeSamePassword           ErrorCode = "same_password"
	ErrorCodeOtpExpired             ErrorCode = "otp_expired"
	ErrorCodeOtpDisabled            ErrorCode = "otp_disabled"
	ErrorCodeOverRequestRateLimit   ErrorCode = "over_request_rate_limit"
	ErrorCodeOverEmailSendRateLimit ErrorCode = "over_email_send_rate_limit"
	ErrorCodeOverSMSSendRateLimit   ErrorCode = "over_sms_send_rate_limit"
	ErrorCodeMfaVerificationFailed  ErrorCode = "mfa_verification_failed"
	ErrorCodeMfaChallengeExpired    ErrorCode = "mfa_challenge_expired"
	ErrorCodeMfaFactorNotFound      ErrorCode = "mfa_factor_not_found"
	ErrorCodeInsufficientAAL        ErrorCode = "insufficient_aal"
	ErrorCodeBadCodeVerifier        ErrorCode = "bad_code_verifier"
	ErrorCodeFlowStateNotFound      ErrorCode = "flow_state_not_found"
	ErrorCodeFlowStateExpired       ErrorCode = "flow_state_expired"
	ErrorCodeSignupDisabled         ErrorCode = "signup_disabled"
	ErrorCodeAnonymousDisabled      ErrorCode = "anonymous_provider_disabled"
	ErrorCodeIdentityAlreadyExists  ErrorCode = "identity_already_exists"
	ErrorCodeSingleIdentityNotDelet ErrorCode = "single_identity_not_deletable"
	ErrorCodeReauthenticationNeeded ErrorCode = "reauthentication_needed"
	ErrorCodeCaptchaFailed          ErrorCode = "captcha_failed"
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

func (e *AuthApiError) Unwrap() error { return e.AuthError }

// AuthRetryableFetchError represents a transient network or 5xx gateway error from GoTrue.
type AuthRetryableFetchError struct {
	*AuthError
}

func (e *AuthRetryableFetchError) Unwrap() error { return e.AuthError }

// AuthWeakPasswordError is returned when GoTrue rejects a password for failing strength policy.
type AuthWeakPasswordError struct {
	*AuthError
	Reasons []string
}

func (e *AuthWeakPasswordError) Unwrap() error { return e.AuthError }

// AuthPKCECodeVerifierMissingError is returned when ExchangeCodeForSession cannot find a stored PKCE verifier.
type AuthPKCECodeVerifierMissingError struct {
	*AuthError
}

func (e *AuthPKCECodeVerifierMissingError) Unwrap() error { return e.AuthError }

// AuthInvalidJwtError is returned when a JWT is malformed, expired, or fails signature verification.
type AuthInvalidJwtError struct {
	*AuthError
}

func (e *AuthInvalidJwtError) Unwrap() error { return e.AuthError }

// AuthRefreshDiscardedError is returned when an in-flight token refresh is discarded because SignOut occurred concurrently.
type AuthRefreshDiscardedError struct {
	*AuthError
}

func (e *AuthRefreshDiscardedError) Unwrap() error { return e.AuthError }

func isRetryableAuthStatus(status int) bool {
	return (status >= 500 && status <= 504) || (status >= 520 && status <= 530)
}

func decodeAuthError(body []byte, status int, statusText string) error {
	var raw struct {
		ErrorCode        string `json:"error_code"`
		Code             any    `json:"code"`
		Msg              string `json:"msg"`
		Message          string `json:"message"`
		ErrorDescription string `json:"error_description"`
		Error            string `json:"error"`
		WeakPassword     *struct {
			Reasons []string `json:"reasons"`
		} `json:"weak_password"`
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

	if isRetryableAuthStatus(status) {
		return &AuthRetryableFetchError{
			AuthError: &AuthError{
				Name:    "AuthRetryableFetchError",
				Message: msg,
				Status:  status,
				Code:    code,
			},
		}
	}

	if code == string(ErrorCodeWeakPassword) || (raw.WeakPassword != nil && len(raw.WeakPassword.Reasons) > 0) {
		var reasons []string
		if raw.WeakPassword != nil {
			reasons = raw.WeakPassword.Reasons
		}
		return &AuthWeakPasswordError{
			AuthError: &AuthError{
				Name:    "AuthWeakPasswordError",
				Message: msg,
				Status:  status,
				Code:    code,
			},
			Reasons: reasons,
		}
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
