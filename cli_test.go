package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIConflictingModes(t *testing.T) {
	if _, err := parseCLI([]string{"--context", "prod-eu", "--export-defaults", "./defaults"}); err == nil {
		t.Fatal("conflicting modes accepted")
	}
	for _, args := range [][]string{{"--context="}, {"--unknown"}, {"unexpected"}} {
		if _, err := parseCLI(args); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
}
func TestCLIVersionBeforeConfig(t *testing.T) {
	opts, err := parseCLI([]string{"--version"})
	if err != nil || !opts.ShowVersion {
		t.Fatal(opts, err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(blocked, []byte("x"), 0600)
	cmd := exec.Command("go", "run", ".", "--version")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+blocked)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "termdevtools dev") {
		t.Fatal(string(output), err)
	}
}
func TestCLIContextSelection(t *testing.T) {
	opts, err := parseCLI([]string{"--context", "prod-eu"})
	if err != nil || opts.ContextName != "prod-eu" {
		t.Fatal(opts, err)
	}
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer s.Close()
	home := t.TempDir()
	dir := filepath.Join(home, "termdevtools")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "contexts.yaml"), []byte("contexts:\n  - {name: known, url: "+s.URL+"}\n"), 0600)
	cmd := exec.Command("go", "run", ".", "--context", "unknown")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Unknown context: unknown") || requests.Load() != 0 {
		t.Fatal(string(output), err, requests.Load())
	}
}
