package ui

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/config"
)

func TestRotatedMTLSKeyCanPromptForNewPassphrase(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte(elasticsearchRoot))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	defer srv.Close()
	other := clusterServer(t)
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	rewriteKey := func(pass string) {
		t.Helper()
		block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), []byte(pass), x509.PEMCipherAES256)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rewriteKey("old-passphrase")
	inv, err := config.ParseContexts([]byte(fmt.Sprintf("contexts:\n - {name: a, url: %s, proxy: none, auth_type: mtls, tls: {verify: false, client_cert: %s, client_key: %s}}\n - {name: b, url: %s, proxy: none}\n", srv.URL, certPath, keyPath, other.URL)), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, screen := startContextController(t, inv)
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.credentialsOpen })
	enterPassphrase := func(pass string) {
		injectText(screen, pass)
		screen.InjectKey(tcell.KeyTab, 0, 0)
		screen.InjectKey(tcell.KeyEnter, 0, 0)
	}
	enterPassphrase("old-passphrase")
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() { c.openPicker(); c.choose("named:b") })
	waitController(t, c, func() bool { return c.active.profile.Name == "b" })
	rewriteKey("new-passphrase")
	controllerUI(c, func() { c.openPicker(); c.choose("named:a") })
	waitController(t, c, func() bool { return c.candidate == nil })
	controllerUI(c, func() {
		if c.active.profile.Name != "b" || c.credentialsOpen {
			t.Error("failed decryption changed session or retried automatically")
		}
	})
	if requests.Load() != 1 {
		t.Fatal("failed key validation sent an HTTP request", requests.Load())
	}
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.credentialsOpen || c.candidate == nil })
	promptOpen := false
	controllerUI(c, func() { promptOpen = c.credentialsOpen })
	if !promptOpen {
		t.Fatal("rotated key cannot prompt for a new passphrase")
	}
	enterPassphrase("new-passphrase")
	waitController(t, c, func() bool { return c.active.profile.Name == "a" })
	if requests.Load() != 2 {
		t.Fatal("successful retry did not send exactly one validation request", requests.Load())
	}
}
