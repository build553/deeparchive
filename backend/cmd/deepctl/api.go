package main

// Thin HTTP client for the DeepArchive Go API. Knows the request/response
// shapes; knows nothing about encryption or chunking — that stays in
// internal/backupclient. Mirrors crates/backup-cli's src/api.rs field for
// field (same endpoints, same JSON shapes), since it talks to the exact
// same API.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"deeparchive/internal/backupclient"
)

type API struct {
	http *http.Client
	base string
}

// NewAPI builds a client. resolveOverrides rewrites specific
// HOST:PORT -> IP:PORT pairs at dial time, exactly like curl's
// --resolve — needed only for local Docker Compose development, where a
// presigned object-storage URL embeds the compose-internal hostname
// "minio", which only the API container can resolve by default.
func NewAPI(base string, resolveOverrides map[string]string) *API {
	// Against a local dev stack, presigned URLs name the compose-internal
	// host "minio", unreachable from the host machine. When the API itself
	// is on this machine, route that name to the published port
	// automatically instead of making every developer pass --resolve.
	if u, err := url.Parse(base); err == nil && isLoopback(u.Hostname()) {
		if _, set := resolveOverrides["minio:9000"]; !set {
			merged := map[string]string{"minio:9000": "127.0.0.1:9000"}
			for k, v := range resolveOverrides {
				merged[k] = v
			}
			resolveOverrides = merged
		}
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if override, ok := resolveOverrides[addr]; ok {
				addr = override
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &API{http: &http.Client{Timeout: 5 * time.Minute, Transport: transport}, base: base}
}

func (a *API) url(path string) string { return a.base + path }

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("request failed (%d): %s: %s", e.Status, e.Code, e.Message)
}

// do sends a JSON request (body may be nil) and decodes a JSON response
// into out (which may be nil for a response whose body isn't needed). A
// non-2xx response always returns *apiError, so callers can inspect
// err.(*apiError).Code the way the device-code poll loop needs to.
func (a *API) do(method, path string, bearer string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.url(path), reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		return &apiError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
	}
	return nil
}

type LoginSession struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  string `json:"access_expires_at"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
}

// Refresh exchanges a refresh token for a fresh access+refresh pair.
// Called on every CLI invocation that uses a stored login rather than
// checking the access token's expiry client-side — simpler, and
// self-correcting if the two clocks disagree. The API rotates the
// refresh token on every use, so the caller must persist the new one.
func (a *API) Refresh(refreshToken string) (*LoginSession, error) {
	var session LoginSession
	err := a.do(http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refreshToken}, &session)
	return &session, err
}

type DeviceCodeStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceCode starts a device-code sign-in — the `deepctl login` flow,
// the same shape `gh auth login` uses: no password ever touches this
// process, only a short code a person confirms in their own browser.
func (a *API) DeviceCode(clientName string) (*DeviceCodeStart, error) {
	var start DeviceCodeStart
	err := a.do(http.MethodPost, "/api/v1/auth/device/code", "", map[string]string{"client_name": clientName}, &start)
	return &start, err
}

// DevicePollResult is one of Pending/SlowDown/Denied/Expired (Session
// nil) or a completed sign-in (Session set).
type DevicePollResult struct {
	Session  *LoginSession
	Pending  bool
	SlowDown bool
	Denied   bool
	Expired  bool
}

func (a *API) DeviceToken(deviceCode string) (*DevicePollResult, error) {
	var session LoginSession
	err := a.do(http.MethodPost, "/api/v1/auth/device/token", "", map[string]string{"device_code": deviceCode}, &session)
	if err == nil {
		return &DevicePollResult{Session: &session}, nil
	}
	apiErr, ok := err.(*apiError)
	if !ok {
		return nil, err
	}
	switch apiErr.Code {
	case "authorization_pending":
		return &DevicePollResult{Pending: true}, nil
	case "slow_down":
		return &DevicePollResult{SlowDown: true}, nil
	case "access_denied":
		return &DevicePollResult{Denied: true}, nil
	case "expired_token":
		return &DevicePollResult{Expired: true}, nil
	default:
		return nil, err
	}
}

type Device struct {
	ID string `json:"id"`
}

// EnrollDevice registers the device. wrapped is the passphrase-encrypted
// vault key; sending it lets the dashboard unlock backups with only the
// passphrase. The passphrase and the raw key never leave this machine.
func (a *API) EnrollDevice(userToken, name, os, clientVersion, publicKeyB64 string, wrapped backupclient.WrappedVaultKey) (string, error) {
	var d Device
	err := a.do(http.MethodPost, "/api/v1/devices/enroll", userToken, map[string]any{
		"name": name, "os": os, "client_version": clientVersion, "public_key": publicKeyB64,
		"wrapped_vault_key": wrapped,
	}, &d)
	return d.ID, err
}

// HasVaultKey reports whether the account already holds this device's
// wrapped vault key.
func (a *API) HasVaultKey(userToken, deviceID string) (bool, error) {
	err := a.do(http.MethodGet, "/api/v1/devices/"+deviceID+"/vault-key", userToken, nil, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == "no_vault_key" {
		return false, nil
	}
	return err == nil, err
}

func (a *API) PutVaultKey(userToken, deviceID string, wrapped backupclient.WrappedVaultKey) error {
	return a.do(http.MethodPut, "/api/v1/devices/"+deviceID+"/vault-key", userToken, map[string]any{"wrapped_vault_key": wrapped}, nil)
}

func (a *API) ApproveDevice(userToken, deviceID string) error {
	return a.do(http.MethodPost, "/api/v1/devices/"+deviceID+"/approve", userToken, nil, nil)
}

type challengeResponse struct {
	ChallengeID string `json:"challenge_id"`
	Nonce       string `json:"nonce"`
}

func (a *API) IssueChallenge(deviceID string) (challengeID, nonceB64 string, err error) {
	var c challengeResponse
	err = a.do(http.MethodPost, "/api/v1/devices/"+deviceID+"/challenge", "", nil, &c)
	return c.ChallengeID, c.Nonce, err
}

func (a *API) AuthenticateDevice(deviceID, challengeID, signatureB64 string) (string, error) {
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	err := a.do(http.MethodPost, "/api/v1/devices/"+deviceID+"/authenticate", "", map[string]string{
		"challenge_id": challengeID, "signature": signatureB64,
	}, &resp)
	return resp.AccessToken, err
}

type ChunkStatus struct {
	Hash      string `json:"hash"`
	Exists    bool   `json:"exists"`
	UploadURL string `json:"upload_url,omitempty"`
}

func (a *API) Negotiate(deviceToken, deviceID string, hashes []string) ([]ChunkStatus, error) {
	var resp struct {
		Chunks []ChunkStatus `json:"chunks"`
	}
	err := a.do(http.MethodPost, "/api/v1/devices/"+deviceID+"/backups/negotiate", deviceToken,
		map[string][]string{"chunk_hashes": hashes}, &resp)
	return resp.Chunks, err
}

type CommitChunk struct {
	Hash           string `json:"hash"`
	CiphertextSize uint64 `json:"ciphertext_size"`
}

type CommitRequest struct {
	ManifestNonce      string        `json:"manifest_nonce"`
	ManifestCiphertext string        `json:"manifest_ciphertext"`
	TotalSize          uint64        `json:"total_size"`
	Chunks             []CommitChunk `json:"chunks"`
}

func (a *API) CommitSnapshot(deviceToken, deviceID string, req CommitRequest) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	err := a.do(http.MethodPost, "/api/v1/devices/"+deviceID+"/backups", deviceToken, req, &resp)
	return resp.ID, err
}

type SnapshotSummary struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at"`
	TotalSize  uint64 `json:"total_size"`
	ChunkCount uint64 `json:"chunk_count"`
}

func (a *API) ListBackups(userToken string) ([]SnapshotSummary, error) {
	var resp struct {
		Snapshots []SnapshotSummary `json:"snapshots"`
	}
	err := a.do(http.MethodGet, "/api/v1/backups", userToken, nil, &resp)
	return resp.Snapshots, err
}

type ChunkLocation struct {
	Hash        string `json:"hash"`
	DownloadURL string `json:"download_url"`
}

type SnapshotDetail struct {
	ManifestNonce      string          `json:"manifest_nonce"`
	ManifestCiphertext string          `json:"manifest_ciphertext"`
	Chunks             []ChunkLocation `json:"chunks"`
}

func (a *API) GetSnapshot(userToken, snapshotID string) (*SnapshotDetail, error) {
	var detail SnapshotDetail
	err := a.do(http.MethodGet, "/api/v1/backups/"+snapshotID, userToken, nil, &detail)
	return &detail, err
}

// PutBytes/GetBytes talk directly to the presigned object-storage URL —
// no Authorization header; the signature embedded in the URL itself is
// the authorization.
func (a *API) PutBytes(presignedURL string, body []byte) error {
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("uploading chunk: %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (a *API) GetBytes(presignedURL string) ([]byte, error) {
	resp, err := a.http.Get(presignedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("downloading chunk: %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
