package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// RequestTimeout bounds one POST (GF-146). Shutdown waits at most this long for in-flight requests.
const RequestTimeout = 5 * time.Second

// PositionBody is the POST /ingest/position wire shape (GF-146). Keys match ingest's PositionRequest.
type PositionBody struct {
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Speed    float64 `json:"speed"`
	Heading  float64 `json:"heading"`
	Accuracy float64 `json:"accuracy"`
	TS       string  `json:"ts"`
}

// Outcome classifies one send (GF-146).
type Outcome int

// Outcomes. 204 is Accepted, other 4xx is Rejected, and 5xx or a transport error is Failed.
const (
	Accepted Outcome = iota
	Rejected
	Failed
)

// Sender sends one position for one device key. It is the seam the pacer uses (GF-148).
// The error, when present, describes the status or error class and never the key.
type Sender interface {
	Send(ctx context.Context, key string, body PositionBody) (Outcome, error)
}

// HTTPSender posts to {api}/ingest/position with the key in X-Device-Key (GF-146).
type HTTPSender struct {
	url    string
	client *http.Client
}

// NewHTTPSender returns a sender that keeps up to conns idle connections per host.
func NewHTTPSender(api string, conns int) *HTTPSender {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = conns
	return &HTTPSender{
		url:    strings.TrimRight(api, "/") + "/ingest/position",
		client: &http.Client{Timeout: RequestTimeout, Transport: tr},
	}
}

// Send posts one position and classifies the response. It never retries (GF-146).
func (s *HTTPSender) Send(ctx context.Context, key string, body PositionBody) (Outcome, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return Failed, fmt.Errorf("encode body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(buf))
	if err != nil {
		return Failed, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Device-Key", key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return Failed, fmt.Errorf("transport error: %s", errorClass(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusNoContent:
		return Accepted, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return Rejected, fmt.Errorf("status %d%s", resp.StatusCode, responseCode(raw))
	default:
		return Failed, fmt.Errorf("status %d%s", resp.StatusCode, responseCode(raw))
	}
}

// responseCode extracts the {"error": "<code>"} value, or "" when absent.
func responseCode(raw []byte) string {
	var m struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Error == "" {
		return ""
	}
	return " code " + m.Error
}

// errorClass names a transport failure without its details.
func errorClass(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "connection"
	}
}
