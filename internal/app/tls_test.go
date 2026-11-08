package app_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/telemetry"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

// writeCA issues a CA and a server cert for 127.0.0.1, like cert-manager's ca-issuer
// (tls.crt, tls.key, ca.crt).
func writeCA(t *testing.T, dir string) (certFile, keyFile, caFile string) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, IsCA: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, _ := x509.ParseCertificate(caDER)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "infra-monitor"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)
	kb, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile, caFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), filepath.Join(dir, "ca.crt")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600))
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))
	return
}

func TestServesHTTPSAndTrustsCA(t *testing.T) {
	cert, key, ca := writeCA(t, t.TempDir())
	t.Setenv("INFRA_MONITOR_SERVER_TLS_CERT_FILE", cert)
	t.Setenv("INFRA_MONITOR_SERVER_TLS_KEY_FILE", key)
	r := startReplica(t, pgtest.NewURL(t), "ca_file: "+ca)
	require.Contains(t, r.url, "https://")

	// The shared outbound client trusts ca_file (same mechanism used for Holmes/LiteLLM in-cluster).
	resp, err := telemetry.HTTPClient(5 * time.Second).Get(r.url + "/healthz")
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, "ok", string(b))

	// Plain HTTP is refused.
	plain := "http://" + r.url[len("https://"):]
	resp, err = telemetry.HTTPClient(2 * time.Second).Get(plain + "/healthz")
	if err == nil {
		resp.Body.Close()
		require.NotEqual(t, 200, resp.StatusCode)
	}
}
