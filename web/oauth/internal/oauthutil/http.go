package oauthutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const MaxResponseSize = 1 << 20

var Client = &http.Client{Timeout: 15 * time.Second}

// JSON validates status and bounds decoding. Errors never include a URL, body,
// or provider credential, since callers may return them to a browser.
func JSON(req *http.Request, target any) error {
	resp, err := Client.Do(req)
	if err != nil {
		return fmt.Errorf("OAuth provider request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OAuth provider returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read OAuth provider response failed")
	}
	if len(body) > MaxResponseSize {
		return fmt.Errorf("OAuth provider response too large")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid OAuth provider response")
	}
	return nil
}
