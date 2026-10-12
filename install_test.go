package main

import (
	"archive/zip"
	"context"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Serve this checkout as a versioned module, then install it from outside
// the repository. Both a wrong module path and broken internal imports make
// the actual go install fail. Dependencies come from Go's existing cache;
// the test does not publish anything or contact GitHub.
func TestGoInstallVersionedModule(t *testing.T) {
	const module = "github.com/gnocent/termdevtools"
	const version = "v0.0.0-installtest"
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goTool += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cache, err := exec.CommandContext(ctx, goTool, "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	versions := filepath.Join(proxy, filepath.FromSlash(module), "@v")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		version + ".mod":  mod,
		version + ".info": []byte(`{"Version":"v0.0.0-installtest","Time":"2026-01-01T00:00:00Z"}`),
	} {
		if err := os.WriteFile(filepath.Join(versions, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := os.Create(filepath.Join(versions, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	err = filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || path == "plans") {
				return filepath.SkipDir
			}
			return nil
		}
		path = filepath.ToSlash(path)
		if path != "go.mod" && path != "go.sum" &&
			!(strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")) &&
			!strings.HasPrefix(path, "refdata/data/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := writer.Create(module + "@" + version + "/" + path)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		return err
	})
	zipErr, closeErr := writer.Close(), archive.Close()
	if err != nil || zipErr != nil || closeErr != nil {
		t.Fatalf("module archive: %v, %v, %v", err, zipErr, closeErr)
	}
	fileURL := func(path string) string { return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() }
	bin := filepath.Join(root, "bin")
	cmd := exec.CommandContext(ctx, goTool, "install", module+"@"+version)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=off", "GOPRIVATE=", "GONOPROXY=none",
		"GOMODCACHE="+filepath.Join(root, "modules"), "GOBIN="+bin,
		"GOPROXY="+fileURL(proxy)+","+fileURL(filepath.Join(strings.TrimSpace(string(cache)), "cache", "download")))
	// Module cache files are read-only. Let Go remove them before TempDir's
	// cleanup rather than leaving a cache behind or failing cleanup.
	t.Cleanup(func() {
		clean := exec.Command(goTool, "clean", "-modcache")
		clean.Dir, clean.Env = cmd.Dir, cmd.Env
		if output, err := clean.CombinedOutput(); err != nil {
			t.Errorf("clean module cache: %v\n%s", err, output)
		}
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v\n%s", err, output)
	}
	name := "termdevtools"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output, err := exec.CommandContext(ctx, filepath.Join(bin, name), "--version").CombinedOutput()
	if err != nil || string(output) != "termdevtools dev\n" {
		t.Fatalf("installed binary --version: %q (%v)", output, err)
	}
}
