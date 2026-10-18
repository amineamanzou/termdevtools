package esclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnusedTLSFilesDoNotBlockRequests(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("connected"))
	})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	httpsServer := httptest.NewTLSServer(handler)
	defer httpsServer.Close()
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for label, ca := range map[string]string{"missing": filepath.Join(dir, "missing.pem"), "directory": dir, "invalid PEM": invalid} {
		for _, tc := range []struct {
			name, url, auth string
			verify          bool
		}{
			{"HTTP verified", httpServer.URL, AuthNone, true},
			{"HTTP unverified", httpServer.URL, AuthNone, false},
			{"HTTP with unused mTLS files", httpServer.URL, AuthMTLS, true},
			{"HTTPS unverified", httpsServer.URL, AuthNone, false},
		} {
			t.Run(tc.name+"/"+label, func(t *testing.T) {
				client, err := New(Params{URL: tc.url, AuthType: tc.auth, Verify: tc.verify,
					CAFile: ca, ClientCert: ca, ClientKey: ca, Proxy: ProxyNone, Timeout: 3 * time.Second})
				if err != nil {
					t.Fatalf("unused TLS files blocked connection: %v", err)
				}
				result, err := client.Execute(context.Background(), "GET", "/", nil)
				if err != nil || result.StatusCode != http.StatusOK || string(result.Body) != "connected" {
					t.Fatalf("request: %+v (%v)", result, err)
				}
				curl := client.CurlCommand("GET", "/", nil)
				if strings.Contains(curl, "--cacert") || (!strings.HasPrefix(tc.url, "https:") &&
					(strings.Contains(curl, "--cert") || strings.Contains(curl, "--key") || strings.Contains(curl, " -k"))) {
					t.Errorf("curl includes unused TLS options: %s", curl)
				}
				if strings.HasPrefix(tc.url, "https:") && !strings.Contains(curl, " -k") {
					t.Errorf("curl must disable server verification: %s", curl)
				}
			})
		}
	}
}

func TestVerifiedHTTPSStillRequiresTrustedCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Params{URL: srv.URL, Verify: true, CAFile: ca, Proxy: ProxyNone})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := client.Execute(context.Background(), "GET", "/", nil); err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("trusted certificate: %+v (%v)", result, err)
	}
	if curl := client.CurlCommand("GET", "/", nil); !strings.Contains(curl, "--cacert") || strings.Contains(curl, " -k") {
		t.Errorf("verified HTTPS curl: %s", curl)
	}
	for _, badCA := range []string{filepath.Join(dir, "missing.pem"), dir} {
		if _, err := New(Params{URL: srv.URL, Verify: true, CAFile: badCA}); err == nil || !strings.Contains(err.Error(), "loading CA") {
			t.Errorf("invalid CA should fail when verification is enabled: %v", err)
		}
	}
	client, err = New(Params{URL: srv.URL, Verify: true, Proxy: ProxyNone})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execute(context.Background(), "GET", "/", nil); err == nil {
		t.Error("untrusted server certificate must still be rejected")
	}
}

func TestUnverifiedHTTPSStillAuthenticatesWithClientCertificate(t *testing.T) {
	certFile, keyFile := pkcs8Paths("rsa-aes256-sha256")
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	trusted := x509.NewCertPool()
	if !trusted.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid client certificate fixture")
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.TLS.PeerCertificates[0].Subject.CommonName))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: trusted}
	srv.StartTLS()
	defer srv.Close()
	params := Params{URL: srv.URL, AuthType: AuthMTLS, Verify: false,
		CAFile: filepath.Join(t.TempDir(), "missing-ca.pem"), ClientCert: certFile, ClientKey: keyFile,
		KeyPassphrase: pkcs8Passphrase, Proxy: ProxyNone, Timeout: 3 * time.Second}
	client, err := New(params)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execute(context.Background(), "GET", "/", nil)
	if err != nil || result.StatusCode != http.StatusOK || !strings.Contains(string(result.Body), "termdevtools test client") {
		t.Fatalf("mTLS authentication: %+v (%v)", result, err)
	}
	if curl := client.CurlCommand("GET", "/", nil); !strings.Contains(curl, "--cert") || !strings.Contains(curl, "--key") || strings.Contains(curl, "--cacert") {
		t.Errorf("unverified mTLS curl: %s", curl)
	}
	params.ClientKey = filepath.Join(t.TempDir(), "missing-key.pem")
	if _, err := New(params); err == nil || !strings.Contains(err.Error(), "loading client certificate") {
		t.Errorf("client credentials must still be required: %v", err)
	}
}
