package relay

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

const maxUpstreamRequests = 5
const maxUpstreamBodyBytes = 8 << 20 // Per request history, including its latest body.

// UpstreamRequestInfo is small enough to list without downloading prompt bodies.
type UpstreamRequestInfo struct {
	Sequence        int            `json:"sequence"`
	Round           int            `json:"round"`
	CapturedAt      time.Time      `json:"captured_at"`
	ChannelKey      string         `json:"channel_key"`
	Model           string         `json:"model"`
	Protocol        model.Protocol `json:"protocol"`
	Method          string         `json:"method"`
	URL             string         `json:"url"`
	BodyBytes       int64          `json:"body_bytes"`
	BodyTruncated   bool           `json:"body_truncated"`
	BodyUnavailable bool           `json:"body_unavailable"`
}

type UpstreamRequestSnapshot struct {
	UpstreamRequestInfo
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

type UpstreamRequestHistory struct {
	Items   []UpstreamRequestInfo `json:"items"`
	Dropped int                   `json:"dropped"`
}

type upstreamCaptureKey struct{}
type upstreamCapture struct {
	state *RequestState
	round int
}
type upstreamLogTransport struct {
	base    http.RoundTripper
	capture upstreamCapture
}

// Wrap a per-call client copy so shared proxy clients stay untouched. RoundTrip
// observes final serialized bodies and authentication for every protocol and stream mode.
func withUpstreamLogging(ctx context.Context, client *http.Client) *http.Client {
	capture, ok := ctx.Value(upstreamCaptureKey{}).(upstreamCapture)
	if !ok {
		return client
	}
	copy := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = &upstreamLogTransport{base: base, capture: capture}
	return &copy
}

func (t *upstreamLogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Context().Err() == nil {
		snapshot := UpstreamRequestSnapshot{UpstreamRequestInfo: UpstreamRequestInfo{
			CapturedAt: time.Now(), Method: request.Method, URL: redactedUpstreamURL(request.URL), BodyBytes: request.ContentLength,
		}, Headers: request.Header.Clone()}
		for key := range snapshot.Headers {
			if sensitiveRequestField(key) {
				snapshot.Headers[key] = []string{"[REDACTED]"}
			}
		}
		// Read a replayable copy, never consume or replace the outgoing Body.
		if request.Body != nil && request.Body != http.NoBody {
			if request.GetBody == nil {
				snapshot.BodyUnavailable = true
			} else if body, err := request.GetBody(); err != nil {
				snapshot.BodyUnavailable = true
			} else {
				data, readErr := io.ReadAll(io.LimitReader(body, maxUpstreamBodyBytes+1))
				body.Close()
				snapshot.BodyUnavailable = readErr != nil
				snapshot.BodyTruncated = len(data) > maxUpstreamBodyBytes
				if snapshot.BodyTruncated {
					data = data[:maxUpstreamBodyBytes]
				}
				snapshot.Body = string(data)
			}
		}
		t.capture.state.recordUpstreamRequest(t.capture.round, snapshot)
	}
	return t.base.RoundTrip(request)
}

func sensitiveRequestField(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	return httpclient.IsSensitiveHeader(name) || normalized == "key" || normalized == "sig" ||
		strings.Contains(normalized, "authorization") || strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") || strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "signature") || strings.Contains(normalized, "cookie")
}

func redactedUpstreamURL(original *url.URL) string {
	copy := *original
	if copy.User != nil {
		copy.User = url.User("[REDACTED]")
	}
	// Preserve the original query encoding and order unless a value needs redaction.
	parts := strings.Split(copy.RawQuery, "&")
	for i, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil || sensitiveRequestField(decoded) {
			parts[i] = key + "=" + url.QueryEscape("[REDACTED]")
		}
	}
	copy.RawQuery = strings.Join(parts, "&")
	copy.Fragment = ""
	return copy.String()
}

func (r *RequestState) recordUpstreamRequest(round int, snapshot UpstreamRequestSnapshot) {
	mu.Lock()
	defer mu.Unlock()
	if r.Round != round || requests[r.ID] != r {
		return
	}
	r.UpstreamRequestCount++
	snapshot.Sequence = r.UpstreamRequestCount
	snapshot.Round = round
	snapshot.ChannelKey = r.TargetChannelKey
	snapshot.Model = r.TargetModel
	snapshot.Protocol = r.TargetProtocol
	r.upstreamRequests = append(r.upstreamRequests, snapshot)
	r.upstreamBodyBytes += len(snapshot.Body)
	for len(r.upstreamRequests) > 1 && (len(r.upstreamRequests) > maxUpstreamRequests || r.upstreamBodyBytes > maxUpstreamBodyBytes) {
		r.upstreamBodyBytes -= len(r.upstreamRequests[0].Body)
		r.upstreamRequests[0] = UpstreamRequestSnapshot{}
		r.upstreamRequests = r.upstreamRequests[1:]
	}
	publishRequestLocked(r)
}

// UpstreamRequests returns retained send attempts, newest first, without their bodies.
func UpstreamRequests(id uint64) (UpstreamRequestHistory, bool) {
	mu.Lock()
	defer mu.Unlock()
	r := requests[id]
	if r == nil {
		return UpstreamRequestHistory{}, false
	}
	history := UpstreamRequestHistory{Items: make([]UpstreamRequestInfo, 0, len(r.upstreamRequests)), Dropped: r.UpstreamRequestCount - len(r.upstreamRequests)}
	for i := len(r.upstreamRequests) - 1; i >= 0; i-- {
		history.Items = append(history.Items, r.upstreamRequests[i].UpstreamRequestInfo)
	}
	return history, true
}

func UpstreamRequest(id uint64, sequence int) (UpstreamRequestSnapshot, bool) {
	mu.Lock()
	defer mu.Unlock()
	if r := requests[id]; r != nil {
		for _, snapshot := range r.upstreamRequests {
			if snapshot.Sequence == sequence {
				snapshot.Headers = snapshot.Headers.Clone()
				return snapshot, true
			}
		}
	}
	return UpstreamRequestSnapshot{}, false
}
