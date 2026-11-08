// Package alertmanager implements the Alertmanager source: webhook payload parsing
// (version 4) and a client for the v2 API (alerts listing for reconcile, silences).
package alertmanager

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/xonoko/infra-monitor/internal/domain"
)

// WebhookPayload is the Alertmanager webhook body (version "4").
type WebhookPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []json.RawMessage `json:"alerts"`
}

// WebhookAlert is one alert in a webhook payload.
type WebhookAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ParseWebhook converts a webhook body into one signal per alert.
func ParseWebhook(body []byte, sourceName string) ([]domain.Signal, error) {
	var p WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("decode alertmanager webhook: %w", err)
	}
	if p.Version != "" && p.Version != "4" {
		return nil, fmt.Errorf("unsupported alertmanager webhook version %q", p.Version)
	}
	out := make([]domain.Signal, 0, len(p.Alerts))
	for i, raw := range p.Alerts {
		var a WebhookAlert
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("decode alert %d: %w", i, err)
		}
		status := strings.ToLower(a.Status)
		if status == "" {
			status = strings.ToLower(p.Status)
		}
		if status != "firing" && status != "resolved" {
			return nil, fmt.Errorf("alert %d: unknown status %q", i, a.Status)
		}
		out = append(out, toSignal(sourceName, status, a.Fingerprint, a.Labels, a.Annotations, a.StartsAt, a.EndsAt, a.GeneratorURL, raw))
	}
	return out, nil
}

func toSignal(source, status, fp string, labels, annotations map[string]string, startsAt, endsAt time.Time, genURL string, raw []byte) domain.Signal {
	if labels == nil {
		labels = map[string]string{}
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	if fp == "" {
		fp = Fingerprint(labels)
	}
	s := domain.Signal{
		Source:       source,
		Fingerprint:  fp,
		Status:       status,
		Labels:       labels,
		Annotations:  annotations,
		StartsAt:     startsAt,
		GeneratorURL: genURL,
		Raw:          raw,
	}
	if s.StartsAt.IsZero() || s.StartsAt.Year() <= 1 {
		s.StartsAt = time.Now()
	}
	// Firing alerts carry a future endsAt (resolve timeout): keep it nil.
	if status == "resolved" && !endsAt.IsZero() && endsAt.Year() > 1 {
		e := endsAt
		s.EndsAt = &e
	}
	return s
}

// Fingerprint computes a stable label-set fingerprint (fnv64a of sorted name/value pairs).
func Fingerprint(labels map[string]string) string {
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)
	h := fnv.New64a()
	for _, k := range names {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0xff})
		_, _ = h.Write([]byte(labels[k]))
		_, _ = h.Write([]byte{0xff})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
