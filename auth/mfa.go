package auth

import (
	"context"
	"net/http"
	"net/url"
)

// MFAClient provides Multi-Factor Authentication operations.
type MFAClient struct {
	client        *Client
	RecoveryCodes *RecoveryCodesClient
}

type MFAEnrollParams struct {
	FactorType   string `json:"factor_type"` // "totp", "phone", "webauthn"
	FriendlyName string `json:"friendly_name,omitempty"`
	Issuer       string `json:"issuer,omitempty"`
	Phone        string `json:"phone,omitempty"`
}

type MFAEnrollResponse struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	FriendlyName string `json:"friendly_name,omitempty"`
	TOTP         *struct {
		QRCode string `json:"qr_code"`
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	} `json:"totp,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type MFAChallengeParams struct {
	FactorID string `json:"-"`
	Channel  string `json:"channel,omitempty"`
}

type MFAChallengeResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ExpiresAt int64  `json:"expires_at"`
}

type MFAVerifyParams struct {
	FactorID    string `json:"-"`
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

type MFAChallengeAndVerifyParams struct {
	FactorID string
	Code     string
	Channel  string
}

type MFAListFactorsResponse struct {
	All      []Factor `json:"all"`
	TOTP     []Factor `json:"totp"`
	Phone    []Factor `json:"phone"`
	WebAuthn []Factor `json:"webauthn"`
}

type MFAUnenrollResponse struct {
	ID string `json:"id"`
}

type AuthenticatorAssuranceLevelResponse struct {
	CurrentLevel               string           `json:"currentLevel"`
	NextLevel                  string           `json:"nextLevel"`
	CurrentAuthenticationMethods []map[string]any `json:"currentAuthenticationMethods"`
}

func (m *MFAClient) Enroll(ctx context.Context, params MFAEnrollParams) (*MFAEnrollResponse, error) {
	token, err := m.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp MFAEnrollResponse
	if err := m.client.doJSON(ctx, http.MethodPost, "/factors", params, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MFAClient) Challenge(ctx context.Context, params MFAChallengeParams) (*MFAChallengeResponse, error) {
	token, err := m.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if params.Channel != "" {
		body["channel"] = params.Channel
	}
	var resp MFAChallengeResponse
	path := "/factors/" + url.PathEscape(params.FactorID) + "/challenge"
	if err := m.client.doJSON(ctx, http.MethodPost, path, body, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MFAClient) Verify(ctx context.Context, params MFAVerifyParams) (*Session, error) {
	token, err := m.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var session Session
	path := "/factors/" + url.PathEscape(params.FactorID) + "/verify"
	if err := m.client.doJSON(ctx, http.MethodPost, path, map[string]any{
		"challenge_id": params.ChallengeID,
		"code":         params.Code,
	}, token, &session); err != nil {
		return nil, err
	}
	if err := m.client.saveSession(ctx, &session, EventMFAChallengeVerified); err != nil {
		return nil, err
	}
	return &session, nil
}

func (m *MFAClient) ChallengeAndVerify(ctx context.Context, params MFAChallengeAndVerifyParams) (*Session, error) {
	ch, err := m.Challenge(ctx, MFAChallengeParams{
		FactorID: params.FactorID,
		Channel:  params.Channel,
	})
	if err != nil {
		return nil, err
	}
	return m.Verify(ctx, MFAVerifyParams{
		FactorID:    params.FactorID,
		ChallengeID: ch.ID,
		Code:        params.Code,
	})
}

func (m *MFAClient) ListFactors(ctx context.Context) (*MFAListFactorsResponse, error) {
	user, err := m.client.GetUser(ctx)
	if err != nil {
		return nil, err
	}
	resp := &MFAListFactorsResponse{All: user.Factors}
	for _, f := range user.Factors {
		if f.Status != "verified" {
			continue
		}
		switch f.FactorType {
		case "totp":
			resp.TOTP = append(resp.TOTP, f)
		case "phone":
			resp.Phone = append(resp.Phone, f)
		case "webauthn":
			resp.WebAuthn = append(resp.WebAuthn, f)
		}
	}
	return resp, nil
}

func (m *MFAClient) Unenroll(ctx context.Context, factorID string) (*MFAUnenrollResponse, error) {
	token, err := m.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp MFAUnenrollResponse
	if err := m.client.doJSON(ctx, http.MethodDelete, "/factors/"+url.PathEscape(factorID), nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MFAClient) GetAuthenticatorAssuranceLevel(ctx context.Context) (*AuthenticatorAssuranceLevelResponse, error) {
	s, err := m.client.GetSession(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return &AuthenticatorAssuranceLevelResponse{}, nil
	}
	_, claims, _, _, err := parseJWTParts(s.AccessToken)
	if err != nil {
		return nil, err
	}
	currentLevel, _ := claims["aal"].(string)
	nextLevel := currentLevel
	if s.User != nil {
		for _, f := range s.User.Factors {
			if f.Status == "verified" {
				nextLevel = "aal2"
				break
			}
		}
	}
	var amr []map[string]any
	if rawAMR, ok := claims["amr"].([]any); ok {
		for _, item := range rawAMR {
			if mp, ok := item.(map[string]any); ok {
				amr = append(amr, mp)
			}
		}
	}
	return &AuthenticatorAssuranceLevelResponse{
		CurrentLevel:                 currentLevel,
		NextLevel:                    nextLevel,
		CurrentAuthenticationMethods: amr,
	}, nil
}

// RecoveryCodesClient exposes GoTrue's 5 MFA recovery code HTTP endpoints under `/factors/recovery-codes*`.
type RecoveryCodesClient struct {
	client *Client
}

type RecoveryCodesResponse struct {
	Codes     []string `json:"codes,omitempty"`
	Enabled   bool     `json:"enabled,omitempty"`
	Remaining int      `json:"remaining,omitempty"`
	Total     int      `json:"total,omitempty"`
}

func (r *RecoveryCodesClient) Generate(ctx context.Context) (*RecoveryCodesResponse, error) {
	token, err := r.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp RecoveryCodesResponse
	if err := r.client.doJSON(ctx, http.MethodPost, "/factors/recovery-codes", nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (r *RecoveryCodesClient) GetStatus(ctx context.Context) (*RecoveryCodesResponse, error) {
	token, err := r.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp RecoveryCodesResponse
	if err := r.client.doJSON(ctx, http.MethodGet, "/factors/recovery-codes", nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (r *RecoveryCodesClient) Verify(ctx context.Context, code string) (*Session, error) {
	token, err := r.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := r.client.doJSON(ctx, http.MethodPost, "/factors/recovery-codes/verify", map[string]any{"code": code}, token, &session); err != nil {
		return nil, err
	}
	if session.AccessToken != "" {
		_ = r.client.saveSession(ctx, &session, EventMFAChallengeVerified)
	}
	return &session, nil
}

func (r *RecoveryCodesClient) Regenerate(ctx context.Context) (*RecoveryCodesResponse, error) {
	token, err := r.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp RecoveryCodesResponse
	if err := r.client.doJSON(ctx, http.MethodPost, "/factors/recovery-codes/regenerate", nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (r *RecoveryCodesClient) Unenroll(ctx context.Context) error {
	token, err := r.client.requireAccessToken(ctx)
	if err != nil {
		return err
	}
	return r.client.doJSON(ctx, http.MethodDelete, "/factors/recovery-codes", nil, token, nil)
}
