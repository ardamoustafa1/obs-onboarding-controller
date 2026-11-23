// Package grafana is a real HTTP client for the Grafana HTTP API
// (https://grafana.com/docs/grafana/latest/developers/http_api/). No
// placeholder/mock transport: every method issues an actual request against
// client.BaseURL, authenticated with a Grafana service account token.
package grafana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{},
	}
}

// APIError is returned for any non-2xx Grafana response, carrying the
// status code so callers (and status Conditions) can distinguish
// "transient, retry" (5xx/429) from "our request is wrong" (4xx).
type APIError struct {
	StatusCode int
	Body       string
	Op         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("grafana %s: HTTP %d: %s", e.Op, e.StatusCode, e.Body)
}

// Retryable reports whether the operator should retry (5xx, 429) versus
// surface the error as a permanent misconfiguration (other 4xx).
func (e *APIError) Retryable() bool {
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.HTTPClient.Do(req)
}

// EnsureFolder makes sure a folder with the given UID/title exists,
// creating it if necessary. Idempotent: a second call is a no-op GET.
func (c *Client) EnsureFolder(ctx context.Context, uid, title string) error {
	resp, err := c.do(ctx, http.MethodGet, "/api/folders/"+uid, nil)
	if err != nil {
		return fmt.Errorf("get folder %s: %w", uid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return apiError("get folder", resp)
	}

	createResp, err := c.do(ctx, http.MethodPost, "/api/folders", map[string]string{
		"uid":   uid,
		"title": title,
	})
	if err != nil {
		return fmt.Errorf("create folder %s: %w", uid, err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK && createResp.StatusCode != http.StatusPreconditionFailed {
		return apiError("create folder", createResp)
	}
	return nil
}

// DashboardResult is what POST /api/dashboards/db returns on success.
type DashboardResult struct {
	UID     string `json:"uid"`
	URL     string `json:"url"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

// UpsertDashboard creates or overwrites a dashboard. dashboard must already
// have its "uid" field set by the caller to a stable value -- Grafana
// assigns a fresh random UID on every call that omits it, which would break
// idempotency (a re-render would produce a new dashboard instead of
// updating the existing one).
func (c *Client) UpsertDashboard(ctx context.Context, folderUID string, dashboard map[string]any) (*DashboardResult, error) {
	if _, ok := dashboard["uid"]; !ok {
		return nil, fmt.Errorf("dashboard payload is missing a stable uid")
	}
	payload := map[string]any{
		"dashboard": dashboard,
		"folderUid": folderUID,
		"overwrite": true,
		"message":   "reconciled by obs-onboarding-controller",
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/dashboards/db", payload)
	if err != nil {
		return nil, fmt.Errorf("upsert dashboard: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("upsert dashboard", resp)
	}
	var result DashboardResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upsert dashboard response: %w", err)
	}
	return &result, nil
}

// GetDashboard fetches a dashboard by UID for drift comparison. Returns
// (nil, nil) if it does not exist.
func (c *Client) GetDashboard(ctx context.Context, uid string) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/dashboards/uid/"+uid, nil)
	if err != nil {
		return nil, fmt.Errorf("get dashboard %s: %w", uid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("get dashboard", resp)
	}
	var body struct {
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode get dashboard response: %w", err)
	}
	return body.Dashboard, nil
}

// DeleteDashboard removes a dashboard by UID. Treats 404 as success (already gone).
func (c *Client) DeleteDashboard(ctx context.Context, uid string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/dashboards/uid/"+uid, nil)
	if err != nil {
		return fmt.Errorf("delete dashboard %s: %w", uid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return apiError("delete dashboard", resp)
	}
	return nil
}

func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{StatusCode: resp.StatusCode, Body: string(body), Op: op}
}
