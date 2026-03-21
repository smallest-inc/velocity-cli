package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps net/http with auth, base URL, and JSON marshaling.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a new API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// APIError represents an error response from the Toggle API.
type APIError struct {
	StatusCode int
	Message    string
	Details    string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
	if e.Details != "" {
		msg += "\n  Details: " + e.Details
	}
	return msg
}

// apiPrefix is the common prefix for all Toggle API routes.
const apiPrefix = "/api/v1/toggle"

func (c *Client) url(path string) string {
	return c.BaseURL + apiPrefix + path
}

func (c *Client) do(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.url(path), reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		var errResp struct {
			Error   string `json:"error"`
			Details string `json:"details"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &errResp) == nil {
			apiErr.Message = errResp.Error
			if apiErr.Message == "" {
				apiErr.Message = errResp.Message
			}
			apiErr.Details = errResp.Details
		}
		if apiErr.Message == "" {
			apiErr.Message = string(respBody)
		}
		return apiErr
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// Get performs a GET request.
func (c *Client) Get(path string, result interface{}) error {
	return c.do(http.MethodGet, path, nil, result)
}

// Post performs a POST request.
func (c *Client) Post(path string, body interface{}, result interface{}) error {
	return c.do(http.MethodPost, path, body, result)
}

// Put performs a PUT request.
func (c *Client) Put(path string, body interface{}, result interface{}) error {
	return c.do(http.MethodPut, path, body, result)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string, result interface{}) error {
	return c.do(http.MethodDelete, path, nil, result)
}
