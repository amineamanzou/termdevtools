package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/config"
)

func TestLegacySessionCompatibility(t *testing.T) {
	srv := clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, srv.URL))
	inventoryPath, _ := config.ContextsPath()
	data := []byte("contexts:\n  - {name: a, url: " + srv.URL + "}\n")
	os.MkdirAll(filepath.Dir(inventoryPath), 0700)
	os.WriteFile(inventoryPath, data, 0600)
	queries, _ := config.QueriesPathForURL(srv.URL)
	vars, _ := config.VariablesPathForURL(srv.URL)
	os.WriteFile(queries, []byte("GET /legacy"), 0600)
	os.WriteFile(vars, []byte("index=legacy\nignored malformed line\n"), 0600)
	controllerUI(c, func() {
		c.openLegacy()
		c.connectLegacy(config.Cluster{URL: srv.URL, AuthType: config.AuthNone, Proxy: "none"}, connectSecrets{})
	})
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() {
		if c.active.profile.Identity != "legacy:"+srv.URL || c.active.app.editor.Text() != "GET /legacy" || c.active.app.variables["index"] != "legacy" {
			t.Error("legacy files lost")
		}
		c.active.app.editor.SetInitialText("GET /legacy-kept")
		c.openPicker()
		c.choose("named:a")
	})
	waitController(t, c, func() bool { return c.active.profile.Identity == "named:a" })
	saved, _ := os.ReadFile(queries)
	inventoryAfter, _ := os.ReadFile(inventoryPath)
	if string(saved) != "GET /legacy-kept" || string(inventoryAfter) != string(data) {
		t.Fatal("modified inventory or lost history")
	}
}
func TestSessionShutdown(t *testing.T) {
	t.Run("signal during candidate", func(t *testing.T) {
		a := clusterServer(t)
		entered := make(chan struct{})
		cancelled := make(chan struct{})
		b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
		defer b.Close()
		c, _ := startContextController(t, controllerInventory(t, a.URL, b.URL))
		controllerUI(c, func() { c.choose("named:a") })
		waitController(t, c, func() bool { return c.active != nil })
		controllerUI(c, func() { c.active.app.editor.SetInitialText("GET /keep"); c.openPicker(); c.choose("named:b") })
		<-entered
		published := make(chan struct{})
		go func() { c.RequestStop(true); close(published) }()
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			t.Fatal("stop blocked")
		}
		<-published
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("candidate not cancelled")
		}
		files, _ := config.ContextWorkPaths("a")
		data, _ := os.ReadFile(files.Queries)
		if string(data) != "GET /keep" {
			t.Fatal(string(data))
		}
		if c.dispatcher.Post(context.Background(), func() { t.Error("after stop") }) {
			t.Fatal("late callback accepted")
		}
	})
	t.Run("save warning then quit", func(t *testing.T) {
		srv := clusterServer(t)
		c, s := startContextController(t, controllerInventory(t, srv.URL))
		controllerUI(c, func() { c.choose("named:a") })
		waitController(t, c, func() bool { return c.active != nil })
		block := filepath.Join(t.TempDir(), "file")
		os.WriteFile(block, []byte("x"), 0600)
		controllerUI(c, func() {
			c.active.app.queriesPath = filepath.Join(block, "queries")
			c.active.app.editor.SetInitialText("GET /keep")
		})
		s.InjectKey(tcell.KeyCtrlC, 0, 0)
		waitForDraw(t, s)
		select {
		case <-c.done:
			t.Fatal("quit before warning")
		default:
		}
		if !strings.Contains(flatScreen(s), "save") {
			t.Fatal("no save warning")
		}
		s.InjectKey(tcell.KeyCtrlC, 0, 0)
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Fatal("second quit blocked")
		}
	})
}
func TestNamedSecretFormKeyboardNavigation(t *testing.T) {
	srv := clusterServer(t)
	inv, err := config.ParseContexts([]byte("contexts:\n  - {name: a, url: "+srv.URL+", auth_type: basic, username: user, proxy: none}\n"), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, s := startContextController(t, inv)
	controllerUI(c, func() { c.choose("named:a") })
	injectText(s, "manual-secret")
	s.InjectKey(tcell.KeyTab, 0, 0)
	s.InjectKey(tcell.KeyEnter, 0, 0)
	waitController(t, c, func() bool { return c.active != nil })
}

func TestEmptyInventoryOffersLegacyAndNewConnection(t *testing.T) {
	srv := clusterServer(t)
	c, _ := startContextController(t, config.Inventory{})
	controllerUI(c, func() {
		c.options.Config.Clusters = []config.Cluster{{URL: srv.URL}}
		entries := c.entries()
		legacy, newConnection := false, false
		for _, entry := range entries {
			legacy = legacy || entry.Identity == "legacy:"+srv.URL
			newConnection = newConnection || entry.Identity == "action:new"
		}
		if !legacy || !newConnection {
			t.Error("empty inventory hides existing workflow")
		}
	})
}
func TestLegacyFailureKeepsForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	c, _ := startContextController(t, config.Inventory{})
	controllerUI(c, func() { c.openLegacy(); c.connectLegacy(config.Cluster{URL: srv.URL, Proxy: "none"}, connectSecrets{}) })
	waitController(t, c, func() bool { return c.candidate == nil })
	controllerUI(c, func() {
		if !c.legacyOpen || c.pickerOpen || !strings.Contains(c.legacy.message.GetText(false), "403") {
			t.Error("legacy form lost after failure")
		}
	})
}
func TestLegacyCandidateCancelledByF6(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
	defer srv.Close()
	c, s := startContextController(t, config.Inventory{})
	controllerUI(c, func() { c.openLegacy(); c.connectLegacy(config.Cluster{URL: srv.URL, Proxy: "none"}, connectSecrets{}) })
	<-entered
	s.InjectKey(tcell.KeyF6, 0, 0)
	waitForDraw(t, s)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("legacy candidate stayed alive")
	}
	controllerUI(c, func() {
		if !c.pickerOpen || c.legacyOpen || c.candidate != nil {
			t.Error("F6 did not return to contexts")
		}
	})
}

func TestSecretFormDoesNotFocusUnderlyingEditor(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	inv, err := config.ParseContexts([]byte("contexts:\n  - {name: a, url: "+a.URL+", proxy: none}\n  - {name: b, url: "+b.URL+", auth_type: basic, username: user, proxy: none}\n"), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, s := startContextController(t, inv)
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() { c.options.App.EnableMouse(true); c.openPicker(); c.choose("named:b") })
	waitForDraw(t, s)
	s.InjectMouse(6, 15, tcell.Button1, 0)
	s.InjectMouse(6, 15, tcell.ButtonNone, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if c.active.app.editor.Primitive().HasFocus() {
			t.Error("mouse focused editor behind secret form")
		}
	})
}

func TestLegacyFormDoesNotFocusUnderlyingEditor(t *testing.T) {
	srv := clusterServer(t)
	c, s := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() { c.options.App.EnableMouse(true); c.openLegacy(); c.legacy.openForm(nil) })
	waitForDraw(t, s)
	s.InjectMouse(2, 15, tcell.Button1, 0)
	s.InjectMouse(2, 15, tcell.ButtonNone, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if c.active.app.editor.Primitive().HasFocus() {
			t.Error("mouse focused editor behind legacy form")
		}
	})
}
