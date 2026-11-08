package app

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// certReloader serves the certificate from disk and reloads it when the files change
// (cert-manager renews certificates in place).
type certReloader struct {
	certFile, keyFile string
	log               *slog.Logger

	mu      sync.RWMutex
	cert    *tls.Certificate
	modTime time.Time
	checked time.Time
}

func newCertReloader(certFile, keyFile string, log *slog.Logger) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile, log: log}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) reload() error {
	st, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("tls cert: %w", err)
	}
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("tls keypair: %w", err)
	}
	r.mu.Lock()
	r.cert, r.modTime = &c, st.ModTime()
	r.mu.Unlock()
	return nil
}

func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	cert, checked, mod := r.cert, r.checked, r.modTime
	r.mu.RUnlock()
	if time.Since(checked) > 30*time.Second {
		r.mu.Lock()
		r.checked = time.Now()
		r.mu.Unlock()
		if st, err := os.Stat(r.certFile); err == nil && !st.ModTime().Equal(mod) {
			if err := r.reload(); err != nil {
				r.log.Warn("tls certificate reload failed, keeping the previous one", "error", err)
			} else {
				r.log.Info("tls certificate reloaded")
				r.mu.RLock()
				cert = r.cert
				r.mu.RUnlock()
			}
		}
	}
	return cert, nil
}

func (r *certReloader) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: r.GetCertificate}
}
