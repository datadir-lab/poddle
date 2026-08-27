package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultDeviceInterval is the RFC 8628 §3.2 fallback poll interval when the
// authorization server's device-authorization response omits `interval`.
const defaultDeviceInterval = 5 * time.Second

// deviceAuthResp decodes the device-authorization endpoint's response
// (RFC 8628 §3.2).
type deviceAuthResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceTokenErrResp decodes a non-2xx token-endpoint response during device
// polling (RFC 8628 §3.5): {"error": "...", "error_description": "..."}.
type deviceTokenErrResp struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// DeviceFlow runs the OAuth 2.0 Device Authorization Grant (RFC 8628): it
// requests a device code from m.DeviceAuthorizationEndpoint, hands the
// verification URI and user code to display (e.g. printed for a headless
// host's operator), and polls m.TokenEndpoint until the user completes
// consent, the authorization server denies the request, the device code
// expires, or ctx is done.
func DeviceFlow(ctx context.Context, hc *http.Client, m Metadata, clientID, clientSecret, scope string, display func(verificationURI, userCode string)) (Token, error) {
	if m.DeviceAuthorizationEndpoint == "" {
		return Token{}, errors.New("oauth: server does not advertise a device_authorization_endpoint")
	}

	da, err := initiateDeviceAuth(ctx, hc, m.DeviceAuthorizationEndpoint, clientID, clientSecret, scope)
	if err != nil {
		return Token{}, err
	}

	verificationURI := da.VerificationURI
	if da.VerificationURIComplete != "" {
		verificationURI = da.VerificationURIComplete
	}
	if display != nil {
		display(verificationURI, da.UserCode)
	}

	interval := defaultDeviceInterval
	if da.Interval > 0 {
		interval = time.Duration(da.Interval) * time.Second
	}
	var deadline <-chan time.Time
	if da.ExpiresIn > 0 {
		timer := time.NewTimer(time.Duration(da.ExpiresIn) * time.Second)
		defer timer.Stop()
		deadline = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-deadline:
			return Token{}, errors.New("oauth: device code expired before authorization completed")
		case <-time.After(interval):
		}

		tok, pending, slowDown, err := pollDeviceToken(ctx, hc, m.TokenEndpoint, da.DeviceCode, clientID, clientSecret)
		switch {
		case err != nil:
			return Token{}, err
		case slowDown:
			interval += 5 * time.Second
		case pending:
			// keep polling at the current interval.
		default:
			return tok, nil
		}
	}
}

// initiateDeviceAuth POSTs the device-authorization request (RFC 8628 §3.1).
func initiateDeviceAuth(ctx context.Context, hc *http.Client, endpoint, clientID, clientSecret, scope string) (deviceAuthResp, error) {
	form := url.Values{"client_id": {clientID}}
	if scope != "" {
		form.Set("scope", scope)
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return deviceAuthResp{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return deviceAuthResp{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return deviceAuthResp{}, fmt.Errorf("oauth: device authorization endpoint %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var da deviceAuthResp
	if err := json.Unmarshal(body, &da); err != nil {
		return deviceAuthResp{}, fmt.Errorf("oauth: decode device authorization response: %w", err)
	}
	if da.DeviceCode == "" || da.UserCode == "" {
		return deviceAuthResp{}, errors.New("oauth: device authorization response missing device_code or user_code")
	}
	return da, nil
}

// pollDeviceToken makes a single device-code poll of the token endpoint
// (RFC 8628 §3.4-3.5). On success it returns the Token. On a terminal
// (non-retryable) failure it returns a non-nil error. authorization_pending
// and slow_down are reported via the pending/slowDown flags rather than as
// errors, so DeviceFlow's poll loop keeps going.
func pollDeviceToken(ctx context.Context, hc *http.Client, tokenEndpoint, deviceCode, clientID, clientSecret string) (tok Token, pending, slowDown bool, err error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {clientID},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, false, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Token{}, false, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode/100 == 2 {
		tok, err = parseTokenResponse(body)
		return tok, false, false, err
	}

	var de deviceTokenErrResp
	if err := json.Unmarshal(body, &de); err != nil {
		return Token{}, false, false, fmt.Errorf("oauth: decode device token error response: %w", err)
	}
	switch de.Error {
	case "authorization_pending":
		return Token{}, true, false, nil
	case "slow_down":
		return Token{}, true, true, nil
	default:
		msg := de.Error
		if msg == "" {
			msg = fmt.Sprintf("http %d", resp.StatusCode)
		}
		return Token{}, false, false, fmt.Errorf("oauth: device flow denied: %s", msg)
	}
}
