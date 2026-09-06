//go:build e2e

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This is deliberately a subprocess test: SSL_CERT_FILE, the TLS stack,
// process execution and both atomic replacements belong to the binary under
// test, not to the Go test runner. No system trust store is changed.
func TestSelfUpdateTLSAndOfflineRollback(t *testing.T) {
	if runtime.GOOS == "darwin" && os.Getenv(compilerEnv) != "tinygo" {
		t.Skip("standard Go's Darwin verifier does not read SSL_CERT_FILE")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Crier test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	target := buildStamped(t, "9.9.9")
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			f := newFakeGitHub(t)
			handler := f.Config.Handler
			f.Close()
			var handshakes atomic.Int64
			f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.TLS == nil || !r.TLS.HandshakeComplete {
					t.Error("request arrived without a completed TLS handshake")
				}
				handshakes.Add(1)
				handler.ServeHTTP(w, r)
			}))
			f.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
			f.StartTLS()
			t.Cleanup(f.Close)
			f.URL = strings.Replace(f.URL, "127.0.0.1", host, 1)
			f.release("v9.9.9", false, target)
			bin := installed(t)
			before, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			untrusted := runAtEnv(t, bin, []string{"SSL_CERT_FILE=", "SSL_CERT_DIR=" + t.TempDir()}, "self-update", "--api-url", f.URL)
			if untrusted.Code == exitOK || (!strings.Contains(untrusted.Stderr, "x509") && !strings.Contains(untrusted.Stderr, "certificate")) {
				t.Fatalf("unknown CA was not rejected: %+v", untrusted)
			}
			if handshakes.Load() != 0 {
				t.Fatal("untrusted request reached the API")
			}
			trusted := runAtEnv(t, bin, []string{"SSL_CERT_FILE=" + caPath}, "self-update", "--api-url", f.URL)
			if trusted.Code != exitOK || versionOf(t, bin) != "9.9.9" || handshakes.Load() < 2 {
				t.Fatalf("trusted update failed: %+v, TLS requests=%d", trusted, handshakes.Load())
			}
			updated, err := os.ReadFile(bin)
			if err != nil || sha256.Sum256(updated) != sha256.Sum256(target) {
				t.Fatalf("updated bytes do not match the selected compiler's fixture: %v", err)
			}
			completed := handshakes.Load()
			plaintext := runAt(t, bin, "self-update", "--check", "--api-url", strings.Replace(f.URL, "https://", "http://", 1))
			if plaintext.Code == exitOK || handshakes.Load() != completed {
				t.Fatalf("plaintext was not refused by the TLS listener: %+v", plaintext)
			}
			// Stop the release service before asking the new binary to restore
			// the original. Rollback must not require network access.
			f.Close()
			rollback := runAt(t, bin, "self-update", "--rollback")
			restored, err := os.ReadFile(bin)
			if rollback.Code != exitOK || err != nil || sha256.Sum256(restored) != sha256.Sum256(before) {
				t.Fatalf("offline rollback did not restore the original bytes: %+v, %v", rollback, err)
			}
		})
	}
}
