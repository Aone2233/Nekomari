package ipinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// userAgent 让上游能识别调用方（部分公共 API 对空 UA 更严格）。
const userAgent = "Nekomari-IPInfo/" + APIVersion + " (+https://github.com/Aone2233/nekomari)"

// maxBodyBytes 限制上游响应体大小，避免异常响应把内存吃满。
const maxBodyBytes = 1 << 20

// defaultHTTPTimeout 是所有上游请求的默认超时；并发/串行调用都靠 ctx 预算兜底。
const defaultHTTPTimeout = 5 * time.Second

// doJSON 执行一次请求并按需解析 JSON。返回的 resp 已关闭 Body，
// 仅用于读取响应头（ip-api.com 的限流头就在那里）。
//
// 成功判定是 2xx 而不是严格的 200：Globalping 创建测量返回的是 202 Accepted。
func doJSON(ctx context.Context, client *http.Client, req *http.Request, out any) (*http.Response, error) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return resp, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp, fmt.Errorf("decode upstream response: %w", err)
	}
	return resp, nil
}

// newJSONRequest 构造一个带 ctx 的 GET 请求。
func newJSONRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
}
