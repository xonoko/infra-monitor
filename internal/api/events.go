package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/pubsub"
)

func chiURLParam(r *http.Request, k string) string { return chi.URLParam(r, k) }

// registerEvents serves GET /api/v1/events (Server-Sent Events).
func (s *Server) registerEvents() {
	if s.deps.Listener == nil || s.deps.Auth == nil {
		return
	}
	s.router.Get(prefix+"/events", func(w http.ResponseWriter, r *http.Request) {
		p, status, err := s.authenticate(r)
		if err != nil {
			writeJSONError(w, status, err.Error())
			return
		}
		if !p.Role.Allows(domain.RoleViewer) {
			writeJSONError(w, http.StatusForbidden, "forbidden")
			return
		}
		filter := 0
		if v := r.URL.Query().Get("incident_id"); v != "" {
			filter, _ = strconv.Atoi(v)
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "retry: 3000\n\n")
		flusher.Flush()

		timeline, unsubT := s.deps.Listener.Subscribe(pubsub.ChannelTimeline, 256)
		defer unsubT()
		stream, unsubS := s.deps.Listener.Subscribe(pubsub.ChannelStream, 256)
		defer unsubS()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		send := func(event, data string) bool {
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}
		matches := func(payload string) bool {
			if filter == 0 {
				return true
			}
			var m struct {
				IncidentID int `json:"incident_id"`
			}
			return json.Unmarshal([]byte(payload), &m) == nil && m.IncidentID == filter
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				if !send("ping", "{}") {
					return
				}
			case m := <-timeline:
				if m == pubsub.Reconnected {
					if !send("resync", "{}") {
						return
					}
					continue
				}
				if matches(m) && !send("timeline", m) {
					return
				}
			case m := <-stream:
				if m == pubsub.Reconnected {
					continue
				}
				if matches(m) && !send("progress", m) {
					return
				}
			}
		}
	})
}
