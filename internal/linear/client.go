package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type Client struct {
	Endpoint string
	apiKey   string
	http     *http.Client

	// backoff knobs, overridable in tests
	maxRetries int
	baseDelay  time.Duration
	sleep      func(ctx context.Context, d time.Duration) error
}

func New(endpoint, apiKey string) *Client {
	return &Client{
		Endpoint:   endpoint,
		apiKey:     apiKey,
		http:       &http.Client{Timeout: 30 * time.Second},
		maxRetries: 5,
		baseDelay:  time.Second,
		sleep:      sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do executes a GraphQL request with exponential backoff on 429/5xx.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if attempt > c.maxRetries {
				return fmt.Errorf("linear: giving up after %d retries: %w", c.maxRetries, lastErr)
			}
			// exponential backoff with jitter: base * 2^(attempt-1) +- 25%
			d := c.baseDelay << (attempt - 1)
			d += time.Duration(rand.Int63n(int64(d)/2+1)) - d/4
			if err := c.sleep(ctx, d); err != nil {
				return err
			}
		}

		retry, err := c.doOnce(ctx, body, out)
		if err == nil {
			return nil
		}
		if !retry {
			return err
		}
		lastErr = err
	}
}

// doOnce performs a single request. retry=true means the error is transient (429/5xx/network).
func (c *Client) doOnce(ctx context.Context, body []byte, out any) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", c.apiKey) // NOTE: raw key, not "Bearer"
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return true, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		io.Copy(io.Discard, resp.Body)
		return true, fmt.Errorf("linear transient error: http %d", resp.StatusCode)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return false, fmt.Errorf("linear auth failed: http %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("linear http %d", resp.StatusCode)
	}
	var env struct {
		Data   json.RawMessage            `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return false, err
	}
	if len(env.Errors) > 0 {
		return false, fmt.Errorf("graphql: %s", env.Errors[0].Message)
	}
	return false, json.Unmarshal(env.Data, out)
}
