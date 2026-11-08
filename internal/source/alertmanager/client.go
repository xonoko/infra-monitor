package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// Client talks to the Alertmanager v2 API.
type Client struct {
	base *url.URL
	http *http.Client
}

// NewClient creates a client. httpClient may be nil (an instrumented client is used).
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid alertmanager url %q", baseURL)
	}
	if httpClient == nil {
		httpClient = telemetry.HTTPClient(30 * time.Second)
	}
	return &Client{base: u, http: httpClient}, nil
}

// URL returns the base URL.
func (c *Client) URL() string { return c.base.String() }

type gettableAlert struct {
	Fingerprint  string            `json:"fingerprint"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Status       struct {
		State string `json:"state"`
	} `json:"status"`
}

// ListAlerts returns every active alert as a firing signal.
func (c *Client) ListAlerts(ctx context.Context, sourceName string) ([]domain.Signal, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "alertmanager.list_alerts", trace.WithAttributes(
		attribute.String("source", sourceName), attribute.String("alertmanager.url", c.base.String())))
	defer span.End()
	q := url.Values{"active": {"true"}, "silenced": {"true"}, "inhibited": {"true"}, "unprocessed": {"true"}}
	var alerts []json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v2/alerts?"+q.Encode(), nil, &alerts); err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	out := make([]domain.Signal, 0, len(alerts))
	for _, raw := range alerts {
		var a gettableAlert
		if err := json.Unmarshal(raw, &a); err != nil {
			telemetry.RecordError(span, err)
			return nil, fmt.Errorf("decode alert: %w", err)
		}
		out = append(out, toSignal(sourceName, "firing", a.Fingerprint, a.Labels, a.Annotations, a.StartsAt, time.Time{}, a.GeneratorURL, raw))
	}
	span.SetAttributes(attribute.Int("alerts", len(out)))
	return out, nil
}

type silenceMatcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

type postableSilence struct {
	Matchers  []silenceMatcher `json:"matchers"`
	StartsAt  time.Time        `json:"startsAt"`
	EndsAt    time.Time        `json:"endsAt"`
	CreatedBy string           `json:"createdBy"`
	Comment   string           `json:"comment"`
}

// CreateSilence creates a silence and returns its id.
func (c *Client) CreateSilence(ctx context.Context, matchers []domain.Matcher, startsAt, endsAt time.Time, createdBy, comment string) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "alertmanager.create_silence", trace.WithAttributes(
		attribute.String("alertmanager.url", c.base.String()), attribute.Int("matchers", len(matchers))))
	defer span.End()
	if len(matchers) == 0 {
		err := fmt.Errorf("a silence needs at least one matcher")
		telemetry.RecordError(span, err)
		return "", err
	}
	if comment == "" {
		comment = "created by infra-monitor"
	}
	if createdBy == "" {
		createdBy = "infra-monitor"
	}
	body := postableSilence{StartsAt: startsAt.UTC(), EndsAt: endsAt.UTC(), CreatedBy: createdBy, Comment: comment}
	for _, m := range matchers {
		sm := silenceMatcher{Name: m.Name, Value: m.Value}
		switch m.Op {
		case "=":
			sm.IsEqual = true
		case "!=":
		case "=~":
			sm.IsEqual, sm.IsRegex = true, true
		case "!~":
			sm.IsRegex = true
		default:
			err := fmt.Errorf("unsupported matcher operator %q", m.Op)
			telemetry.RecordError(span, err)
			return "", err
		}
		body.Matchers = append(body.Matchers, sm)
	}
	var resp struct {
		SilenceID string `json:"silenceID"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v2/silences", body, &resp); err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	span.SetAttributes(attribute.String("silence.id", resp.SilenceID))
	return resp.SilenceID, nil
}

// ExpireSilence expires a silence now.
func (c *Client) ExpireSilence(ctx context.Context, id string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "alertmanager.expire_silence", trace.WithAttributes(attribute.String("silence.id", id)))
	defer span.End()
	if err := c.do(ctx, http.MethodDelete, "/api/v2/silence/"+url.PathEscape(id), nil, nil); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("alertmanager %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("alertmanager %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return fmt.Errorf("alertmanager %s %s: HTTP %d: %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, msg)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("alertmanager %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}
