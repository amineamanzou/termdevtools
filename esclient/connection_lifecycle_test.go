package esclient

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientKeyPassphraseRequired(t *testing.T) {
	cert, key := pkcs8Paths("rsa-aes256-sha256")
	data, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	der, err := decryptPKCS8(block.Bytes, pkcs8Passphrase)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := x509.EncryptPEMBlock(rand.Reader, "PRIVATE KEY", der, []byte(pkcs8Passphrase), x509.PEMCipherAES256)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(t.TempDir(), "legacy.key")
	plainPath := filepath.Join(t.TempDir(), "plain.key")
	os.WriteFile(legacyPath, pem.EncodeToMemory(legacy), 0600)
	os.WriteFile(plainPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600)
	for _, path := range []string{key, legacyPath} {
		_, err := loadClientCertificate(cert, path, "")
		if !errors.Is(err, ErrKeyPassphraseRequired) {
			t.Fatalf("missing typed passphrase error: %v", err)
		}
		_, err = loadClientCertificate(cert, path, "incorrect-passphrase")
		if !errors.Is(err, ErrInvalidKeyPassphrase) {
			t.Errorf("incorrect passphrase lacks typed error for %s: %v", path, err)
		}
	}
	if _, err = loadClientCertificate(cert, plainPath, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = loadClientCertificate(cert, plainPath+"-missing", ""); err == nil || errors.Is(err, ErrKeyPassphraseRequired) {
		t.Fatal(err)
	}
}
func TestCloseIdleConnections(t *testing.T) {
	closed := make(chan struct{}, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.Start()
	defer srv.Close()
	c, err := New(Params{URL: srv.URL, Proxy: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Execute(context.Background(), "GET", "/", nil); err != nil {
		t.Fatal(err)
	}
	c.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle connection retained")
	}
}
