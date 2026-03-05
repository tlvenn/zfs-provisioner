package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tlvenn/zfs-provisioner/internal/config"
)

// Client sends provisioning requests to a remote server
type Client struct {
	ServerURL  string
	MaxRetry   time.Duration
	HTTPClient *http.Client
}

// NewClient creates a client for the given server URL
func NewClient(serverURL string) *Client {
	return &Client{
		ServerURL:  serverURL,
		MaxRetry:   2 * time.Minute,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Provision sends a provisioning request to the remote server
func (c *Client) Provision(cfg *config.Config) error {
	req := buildRequest(cfg)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.doWithRetry(c.ServerURL+"/provision", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("server rejected request: %s", string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var provResp config.ProvisionResponse
	if err := json.Unmarshal(respBody, &provResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	hasError := false
	for _, result := range provResp.Results {
		switch result.Action {
		case "created":
			fmt.Fprintf(os.Stdout, "created %s\n", result.Name)
		case "updated":
			fmt.Fprintf(os.Stdout, "updated %s\n", result.Name)
			for _, ch := range result.Changes {
				fmt.Fprintf(os.Stdout, "  %s\n", ch)
			}
		case "unchanged":
			// silent
		case "error":
			fmt.Fprintf(os.Stderr, "error provisioning %s: %s\n", result.Name, result.Error)
			hasError = true
		}
	}

	if hasError {
		return fmt.Errorf("one or more datasets failed to provision")
	}

	return nil
}

// buildRequest converts a parsed Config into a ProvisionRequest for the server.
// Reconstructs the nested dataset structure expected by the server API.
func buildRequest(cfg *config.Config) config.ProvisionRequest {
	datasets := make(map[string]interface{})
	parentPrefix := cfg.Parent + "/"

	for _, ds := range cfg.Datasets {
		if !strings.HasPrefix(ds.Name, parentPrefix) {
			continue
		}
		relName := ds.Name[len(parentPrefix):]

		// Marshal properties via JSON to use struct tags (omitempty)
		propsJSON, _ := json.Marshal(ds.Properties)
		var propsMap map[string]interface{}
		json.Unmarshal(propsJSON, &propsMap)

		// Build nested structure for multi-segment paths (e.g., "postgres/data")
		parts := strings.Split(relName, "/")
		target := datasets
		for _, part := range parts[:len(parts)-1] {
			if _, ok := target[part]; !ok {
				target[part] = make(map[string]interface{})
			}
			target = target[part].(map[string]interface{})
		}
		target[parts[len(parts)-1]] = propsMap
	}

	return config.ProvisionRequest{
		Parent:   cfg.Parent,
		Defaults: cfg.Defaults,
		Datasets: datasets,
	}
}

// doWithRetry sends a POST request with exponential backoff retry
func (c *Client) doWithRetry(url string, body []byte) (*http.Response, error) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second
	deadline := time.Now().Add(c.MaxRetry)

	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		resp, err := c.HTTPClient.Post(url, "application/json", bytes.NewReader(body))
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Cap sleep to remaining time so we don't overshoot the deadline
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := backoff
		if sleep > remaining {
			sleep = remaining
		}

		fmt.Fprintf(os.Stderr, "attempt %d: server unreachable (%v), retrying in %s...\n", attempt, err, sleep)

		time.Sleep(sleep)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return nil, fmt.Errorf("server unreachable after %s: %w", c.MaxRetry, lastErr)
}
