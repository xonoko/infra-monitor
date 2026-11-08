package telemetry

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync"
)

var (
	transportMu   sync.RWMutex
	baseTransport http.RoundTripper = http.DefaultTransport
)

// ConfigureOutboundTLS makes every client from HTTPClient trust caFile (PEM) in addition
// to the system roots. An empty path keeps the system roots only.
func ConfigureOutboundTLS(caFile string) error {
	if caFile == "" {
		return nil
	}
	pem, err := os.ReadFile(caFile) //nolint:gosec // operator-provided path
	if err != nil {
		return fmt.Errorf("read ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("ca_file %s contains no PEM certificates", caFile)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transportMu.Lock()
	baseTransport = t
	transportMu.Unlock()
	return nil
}

// OutboundTransport is the base transport (with the extra CA) without tracing.
func OutboundTransport() http.RoundTripper {
	transportMu.RLock()
	defer transportMu.RUnlock()
	return baseTransport
}
