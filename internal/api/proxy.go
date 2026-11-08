package api

import (
	"net"
	"net/http"
	"strings"
)

// forwardedFor sets RemoteAddr from the LAST X-Forwarded-For entry: the address our trusted
// proxy (the ingress) saw. Earlier entries are client-controlled and ignored, unlike chi's
// deprecated RealIP. Only enabled with server.trust_proxy.
func forwardedFor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(ip) != nil {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			}
		}
		next.ServeHTTP(w, r)
	})
}
