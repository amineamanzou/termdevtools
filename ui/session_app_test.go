package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/i18n"
)

func TestWorkspaceIsolation(t *testing.T) {
	files := config.ContextFiles{Queries: filepath.Join(t.TempDir(), "queries.txt"), Variables: filepath.Join(t.TempDir(), "variables.txt")}
	w, err := prepareWorkspace(files, "GET /", nil)
	if err != nil || w.Text != "GET /" || len(w.Variables) != 0 {
		t.Fatal(w, err)
	}
	if _, err := os.Stat(files.Queries); !os.IsNotExist(err) {
		t.Fatal("created absent file")
	}
	os.WriteFile(files.Queries, []byte("GET /a"), 0600)
	os.WriteFile(files.Variables, []byte("index=a\n"), 0600)
	w, err = prepareWorkspace(files, "GET /", nil)
	if err != nil || w.Text != "GET /a" || w.Variables["index"] != "a" {
		t.Fatal(w, err)
	}
	os.Remove(files.Queries)
	os.Mkdir(files.Queries, 0700)
	if _, err = prepareWorkspace(files, "GET /", nil); err == nil {
		t.Fatal("unreadable queries accepted")
	}
}
func TestContextVariablesStrict(t *testing.T) {
	vars, err := parseContextVariables([]byte("# comment\r\nindex=a=b\r\nempty=\n"))
	if err != nil || vars["index"] != "a=b" || vars["empty"] != "" {
		t.Fatal(vars, err)
	}
	for _, bad := range []string{"DO_NOT_LEAK", "bad-name=DO_NOT_LEAK", "a=1\na=DO_NOT_LEAK"} {
		_, err := parseContextVariables([]byte(bad))
		if err == nil || strings.Contains(err.Error(), "DO_NOT_LEAK") {
			t.Fatal(err)
		}
	}
}
func TestSaveBeforeSwitch(t *testing.T) {
	a := &App{editor: NewEditor(i18n.For(i18n.EN)), msgs: i18n.For(i18n.EN), queriesPath: filepath.Join(t.TempDir(), "queries"), savedText: "GET /"}
	a.editor.SetInitialText("GET /")
	if err := a.SaveBeforeSwitch(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.queriesPath); !os.IsNotExist(err) {
		t.Fatal("unchanged buffer written")
	}
	a.editor.SetInitialText("GET /a")
	if err := a.SaveBeforeSwitch(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a.queriesPath)
	if string(data) != "GET /a" {
		t.Fatal(string(data))
	}
	os.Remove(a.queriesPath)
	os.Mkdir(a.queriesPath, 0700)
	a.editor.SetInitialText("GET /b")
	if err := a.SaveBeforeSwitch(); err == nil || a.editor.Text() != "GET /b" {
		t.Fatal("lost unsaved buffer")
	}
	a.queriesLoadFailed = true
	if err := a.SaveBeforeSwitch(); err == nil {
		t.Fatal("overwrote failed historical load")
	}
}
func TestSnapshotUTF8(t *testing.T) {
	files := config.ContextFiles{Queries: filepath.Join(t.TempDir(), "queries"), Variables: filepath.Join(t.TempDir(), "vars")}
	text := "# réseau 😀\nGET /"
	for _, c := range []struct{ input, want int }{{len("# réseau ") + 2, len("# réseau ")}, {999, len(text)}, {-1, 0}} {
		w, err := prepareWorkspace(files, "", &editorSnapshot{Text: text, Cursor: c.input})
		if err != nil || w.Text != text || w.Cursor != c.want {
			t.Fatal(w, err)
		}
	}
}
