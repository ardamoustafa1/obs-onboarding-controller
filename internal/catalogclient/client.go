// Package catalogclient is a real HTTP client for the catalog REST API
// (internal/catalog). No mock transport: every method issues an actual
// request against Client.BaseURL.
package catalogclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/acme/obs-onboarding-controller/internal/catalogapi"
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

type APIError struct {
	StatusCode int
	Body       string
	Op         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("catalog %s: HTTP %d: %s", e.Op, e.StatusCode, e.Body)
}

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
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.HTTPClient.Do(req)
}

// Upsert registers or updates a service's catalog record.
func (c *Client) Upsert(ctx context.Context, rec catalogapi.ServiceRecord) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/services", rec)
	if err != nil {
		return fmt.Errorf("upsert catalog record: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError("upsert", resp)
	}
	return nil
}

// Decommission marks a service's catalog record decommissioned (a soft
// delete kept for audit trail; see internal/catalog.Store.Decommission).
func (c *Client) Decommission(ctx context.Context, environment, name string) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/services/"+environment+"/"+name+"/decommission", nil)
	if err != nil {
		return fmt.Errorf("decommission catalog record: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Already gone (or never registered, e.g. an earlier reconcile
		// failed before the first upsert) -- nothing to do.
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return apiError("decommission", resp)
	}
	return nil
}

func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{StatusCode: resp.StatusCode, Body: string(body), Op: op}
}
