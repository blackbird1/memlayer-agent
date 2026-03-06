package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func doJSONRequest(
	ctx context.Context,
	httpClient *http.Client,
	method string,
	endpoint string,
	payload any,
	headers map[string]string,
) ([]byte, error) {
	startedAt := time.Now()
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("HTTP request failed", "component", "api_client", "method", method, "url", endpoint, "duration_ms", time.Since(startedAt).Milliseconds(), "error", err)
		return nil, fmt.Errorf("request %s %s failed: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logger.Error("HTTP request returned non-2xx", "component", "api_client", "method", method, "url", endpoint, "status_code", resp.StatusCode, "duration_ms", time.Since(startedAt).Milliseconds())
		return nil, fmt.Errorf("%s %s failed with %d: %s", method, endpoint, resp.StatusCode, truncate(raw, 500))
	}
	logger.Info("HTTP request completed", "component", "api_client", "method", method, "url", endpoint, "status_code", resp.StatusCode, "duration_ms", time.Since(startedAt).Milliseconds())
	return raw, nil
}

func truncate(raw []byte, max int) string {
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "...(truncated)"
}
