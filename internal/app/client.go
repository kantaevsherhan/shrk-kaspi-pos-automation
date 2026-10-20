package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (a *App) kaspiRequest(ctx context.Context, method, path string, headers http.Header, body []byte) (map[string]any, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header = headers.Clone()
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, fmt.Errorf("Kaspi returned HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, 2<<20)
	var result map[string]any
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, resp, fmt.Errorf("Kaspi returned invalid JSON (HTTP %d): %w", resp.StatusCode, err)
	}
	if result == nil {
		return nil, resp, errors.New("Kaspi returned an empty response")
	}
	return result, resp, nil
}
func (a *App) qrRequest(ctx context.Context, method, path string, s *KaspiSession, body any) (map[string]any, error) {
	full := a.Cfg.QRPayURL + path
	var bytes []byte
	var err error
	if body != nil {
		bytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	headers, err := a.Cfg.signedHeaders(full, s, string(bytes))
	if err != nil {
		return nil, err
	}
	result, _, err := a.kaspiRequest(ctx, method, full, headers, bytes)
	return result, err
}
func obj(value any) map[string]any {
	m, _ := value.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
func numberValue(value any) float64 {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Float64()
		return n
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}
func field(m map[string]any, keys ...string) any {
	var v any = m
	for _, key := range keys {
		v = obj(v)[key]
	}
	return v
}
func kaspiOK(m map[string]any) bool {
	switch code := m["StatusCode"].(type) {
	case json.Number:
		n, err := code.Float64()
		return err == nil && n == 0
	case float64:
		return code == 0
	case int:
		return code == 0
	default:
		return false
	}
}
func query(path, key, value string) string { return path + "?" + key + "=" + url.QueryEscape(value) }
func clientPhone(raw string) bool {
	if len(raw) != 10 && len(raw) != 11 {
		return false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func safeID(raw string) bool {
	if raw == "" {
		return false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func normalURL(raw string) string {
	if strings.HasPrefix(raw, "https://qr.kaspi.kz/") {
		return strings.Replace(raw, "https://qr.kaspi.kz/", "https://pay.kaspi.kz/pay/", 1)
	}
	return raw
}
