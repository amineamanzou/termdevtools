package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/credentials"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/rivo/tview"
)

func controllerInventory(t *testing.T, urls ...string) config.Inventory {
	t.Helper()
	var b strings.Builder
	b.WriteString("contexts:\n")
	for i, url := range urls {
		fmt.Fprintf(&b, "  - {name: %c, url: %s, proxy: none}\n", 'a'+i, url)
	}
	inv, err := config.ParseContexts([]byte(b.String()), "contexts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return inv
}
func startContextController(t *testing.T, inv config.Inventory) (*SessionController, tcell.SimulationScreen) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	app := tview.NewApplication().SetScreen(s)
	c := NewSessionController(ControllerOptions{App: app, Config: &config.Config{Language: i18n.EN, DefaultTimeoutSeconds: 2}, Inventory: inv, Paths: Paths{Starter: "GET /"}, Credentials: credentials.NewStore(func(string) (string, bool) { return "", false })})
	if err := c.start(""); err != nil {
		t.Fatal(err)
	}
	startTestApplication(t, app, s)
	t.Cleanup(c.finish)
	waitForDraw(t, s)
	return c, s
}
func controllerUI(c *SessionController, fn func()) { c.options.App.QueueUpdateDraw(fn) }
func waitController(t *testing.T, c *SessionController, condition func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		ok := false
		controllerUI(c, func() { ok = condition() })
		if ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("controller condition not reached")
		default:
			runtime.Gosched()
		}
	}
}
func clusterServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(elasticsearchRoot)) }))
	t.Cleanup(s.Close)
	return s
}
func TestSwitchContextsRestoresWork(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	c, s := startContextController(t, controllerInventory(t, a.URL, b.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil && c.active.profile.Identity == "named:a" })
	controllerUI(c, func() {
		c.active.app.editor.SetInitialText("# réseau 😀\nGET /a")
		c.active.app.editor.SelectRange(len("# réseau 😀\nGET /a"), len("# réseau 😀\nGET /a"))
		c.active.app.catLive["indices"] = []string{"from-a"}
		c.openPicker()
		c.choose("named:b")
	})
	waitController(t, c, func() bool { return c.active.profile.Identity == "named:b" })
	files, _ := config.ContextWorkPaths("a")
	os.WriteFile(files.Variables, []byte("index=updated\n"), 0600)
	controllerUI(c, func() { c.active.app.editor.SetInitialText("GET /b"); c.openPicker(); c.choose("named:a") })
	waitController(t, c, func() bool { return c.active.profile.Identity == "named:a" })
	controllerUI(c, func() {
		snap := c.active.app.Snapshot()
		if snap.Text != "# réseau 😀\nGET /a" || snap.Cursor != len(snap.Text) || c.active.app.variables["index"] != "updated" || len(c.active.app.catLive) != 0 {
			t.Error(snap, c.active.app.variables, c.active.app.catLive)
		}
	})
	if !strings.Contains(flatScreen(s), "a · ES") {
		t.Fatal("wrong target header", flatScreen(s))
	}
	data, _ := os.ReadFile(files.Queries)
	if string(data) != "# réseau 😀\nGET /a" {
		t.Fatal(string(data))
	}
}
func TestSwitchFailureKeepsCurrent(t *testing.T) {
	for _, failure := range []string{"http", "file", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			a := clusterServer(t)
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "http" {
					w.WriteHeader(403)
				} else {
					w.Write([]byte(elasticsearchRoot))
				}
			}))
			defer bad.Close()
			c, _ := startContextController(t, controllerInventory(t, a.URL, bad.URL))
			controllerUI(c, func() { c.choose("named:a") })
			waitController(t, c, func() bool { return c.active != nil })
			if failure == "file" {
				files, _ := config.ContextWorkPaths("b")
				os.MkdirAll(files.Queries, 0700)
			}
			controllerUI(c, func() {
				c.active.app.editor.SetInitialText("GET /keep")
				c.openPicker()
				if failure == "cancel" {
					c.closePicker()
				} else {
					c.choose("named:b")
				}
			})
			waitController(t, c, func() bool { return c.candidate == nil })
			controllerUI(c, func() {
				if c.active.profile.Identity != "named:a" || c.active.app.editor.Text() != "GET /keep" {
					t.Error("replaced current session")
				}
			})
		})
	}
}
func TestSwitchSaveFailure(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, a.URL, b.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(parent, []byte("x"), 0600)
	controllerUI(c, func() {
		c.active.app.queriesPath = filepath.Join(parent, "queries")
		c.active.app.editor.SetInitialText("GET /keep")
		c.openPicker()
		c.choose("named:b")
	})
	waitController(t, c, func() bool { return c.candidate == nil })
	controllerUI(c, func() {
		if c.active.profile.Identity != "named:a" || c.active.app.editor.Text() != "GET /keep" {
			t.Error("save failure discarded work")
		}
	})
}
func TestSwitchRapidSelections(t *testing.T) {
	a := clusterServer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Write([]byte(elasticsearchRoot))
	}))
	defer b.Close()
	c, _ := startContextController(t, controllerInventory(t, a.URL, b.URL))
	controllerUI(c, func() { c.choose("named:b") })
	<-entered
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil && c.active.profile.Identity == "named:a" })
	close(release)
	controllerUI(c, func() {
		if c.active.profile.Identity != "named:a" {
			t.Error("old candidate activated")
		}
	})
}
func TestLateAuthFailureDoesNotForgetNewSecret(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			close(entered)
			<-release
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(elasticsearchRoot))
	}))
	defer srv.Close()
	inv, err := config.ParseContexts([]byte(fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none, auth_type: basic, username: user}\n", srv.URL)), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := startContextController(t, inv)
	d, _ := inv.Lookup("a")
	controllerUI(c, func() {
		c.options.Credentials.Accept(d, credentials.Values{Password: "old"}, []credentials.Field{credentials.FieldPassword})
		c.choose("named:a")
	})
	<-entered
	controllerUI(c, func() {
		c.options.Credentials.Accept(d, credentials.Values{Password: "new"}, []credentials.Field{credentials.FieldPassword})
		c.choose("named:a")
	})
	waitController(t, c, func() bool { return c.active != nil })
	close(release)
	controllerUI(c, func() {
		v, _ := c.options.Credentials.Resolve(d, false)
		if v.Password != "new" {
			t.Error("old 401 invalidated new secrets")
		}
	})
}
func TestPickerSuspendsExecution(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte(elasticsearchRoot)) }))
	defer srv.Close()
	c, s := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	s.InjectKey(tcell.KeyF6, 0, 0)
	s.InjectKey(tcell.KeyCtrlE, 0, 0)
	s.InjectKey(tcell.KeyTab, 0, 0)
	waitForDraw(t, s)
	if requests.Load() != 1 {
		t.Fatal("executed in picker")
	}
	controllerUI(c, func() { c.choose("named:a") })
	if requests.Load() != 1 {
		t.Fatal("reconnected unchanged active")
	}
}

func TestSwitchDropsLateResults(t *testing.T) {
	for _, kind := range []string{"GET", "CAT"} {
		t.Run(kind, func(t *testing.T) {
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.Write([]byte(elasticsearchRoot))
					return
				}
				close(entered)
				<-r.Context().Done()
				close(cancelled)
				<-release
				w.Write([]byte("old-response"))
			}))
			defer a.Close()
			defer close(release)
			b := clusterServer(t)
			c, _ := startContextController(t, controllerInventory(t, a.URL, b.URL))
			controllerUI(c, func() { c.choose("named:a") })
			waitController(t, c, func() bool { return c.active != nil })
			controllerUI(c, func() {
				if kind == "GET" {
					c.active.app.editor.SetInitialText("GET /slow")
					c.active.app.executeCurrent()
				} else {
					c.active.app.fetchCatColumns("indices")
				}
			})
			<-entered
			controllerUI(c, func() { c.openPicker(); c.choose("named:b") })
			waitController(t, c, func() bool { return c.active.profile.Identity == "named:b" })
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("old request not cancelled")
			}
			controllerUI(c, func() { c.openPicker(); c.choose("named:a") })
			waitController(t, c, func() bool { return c.active.profile.Identity == "named:a" })
			controllerUI(c, func() {
				if len(c.active.app.catLive) != 0 {
					t.Error("old CAT cache retained")
				}
				if c.active.app.result.view.GetText(false) != "" {
					t.Error("old result retained")
				}
			})
		})
	}
}
func TestSwitchCurrentAuthFailureForgetsManualSecret(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer s.Close()
	inv, err := config.ParseContexts([]byte(fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none, auth_type: basic, username: user}\n", s.URL)), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := startContextController(t, inv)
	d, _ := inv.Lookup("a")
	controllerUI(c, func() {
		c.options.Credentials.Accept(d, credentials.Values{Password: "bad"}, []credentials.Field{credentials.FieldPassword})
		c.choose("named:a")
	})
	waitController(t, c, func() bool { return c.candidate == nil })
	controllerUI(c, func() {
		_, missing := c.options.Credentials.Resolve(d, false)
		if len(missing) != 1 {
			t.Error("refused credential retained")
		}
	})
}
func TestSwitchSecretPromptCanBeCancelled(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	inv, err := config.ParseContexts([]byte(fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none}\n  - {name: b, url: %s, proxy: none, auth_type: basic, username: user}\n", a.URL, b.URL)), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := startContextController(t, inv)
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() {
		c.openPicker()
		c.choose("named:b")
		if !c.credentialsOpen {
			t.Error("did not ask for secret")
		}
		c.closePicker()
		if c.active.profile.Identity != "named:a" || c.candidate != nil {
			t.Error("cancelled prompt changed session")
		}
	})
}

func contextInventoryFile(t *testing.T, c *SessionController, data string) {
	t.Helper()
	if c.options.ContextsPath == "" {
		c.options.ContextsPath = filepath.Join(t.TempDir(), "contexts.yaml")
	}
	if err := os.WriteFile(c.options.ContextsPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestReloadInvalidInventory(t *testing.T) {
	srv := clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	contextInventoryFile(t, c, "contexts: [invalid")
	files, _ := config.ContextWorkPaths("a")
	os.MkdirAll(filepath.Dir(files.Variables), 0700)
	os.WriteFile(files.Variables, []byte("DO_NOT_LEAK"), 0600)
	controllerUI(c, func() {
		before := c.active.app.client
		c.reload()
		if c.active.app.client != before || len(c.inventory.Definitions()) != 1 {
			t.Error("invalid reload replaced session")
		}
		message := c.picker.message.GetText(false)
		if !strings.Contains(message, "contexts.yaml") || !strings.Contains(message, "variables.txt") || strings.Contains(message, "DO_NOT_LEAK") {
			t.Error(message)
		}
	})
}
func TestReloadChangedActiveContext(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, a.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	contextInventoryFile(t, c, fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none, labels: {env: changed}}\n", a.URL))
	controllerUI(c, func() {
		before := c.active.app.client
		c.reload()
		if c.active.app.client != before || c.entries()[0].Changed || c.entries()[0].Labels["env"] != "changed" {
			t.Error("labels changed connection")
		}
	})
	contextInventoryFile(t, c, fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none}\n", b.URL))
	controllerUI(c, func() {
		before := c.active.app.client
		c.reload()
		if c.active.app.client != before || !c.entries()[0].Changed {
			t.Error("configuration change not marked")
		}
		c.openPicker()
		c.choose("named:a")
	})
	waitController(t, c, func() bool { return c.active.profile.Cluster.URL == b.URL })
}
func TestReloadRemovedActiveContext(t *testing.T) {
	srv := clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	contextInventoryFile(t, c, "contexts: []\n")
	controllerUI(c, func() {
		c.active.app.editor.SetInitialText("GET /keep")
		c.reload()
		c.openPicker()
		c.picker.filter.SetText("no match")
		if len(c.picker.shown) != 1 || !c.picker.shown[0].Removed || !c.picker.shown[0].Active {
			t.Error(c.picker.shown)
		}
		c.picker.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
		if c.candidate != nil {
			t.Error("reconnected removed entry")
		}
		if err := c.active.app.SaveBeforeSwitch(); err != nil {
			t.Error(err)
		}
	})
}
func TestReloadCancelsCandidate(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
	defer srv.Close()
	c, _ := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	<-entered
	contextInventoryFile(t, c, "contexts: []\n")
	controllerUI(c, func() {
		c.reload()
		if c.candidate != nil || c.active != nil {
			t.Error("stale candidate retained")
		}
	})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("candidate not cancelled")
	}
}

func TestReloadReconnectKeepsCurrentEditor(t *testing.T) {
	a, b := clusterServer(t), clusterServer(t)
	c, _ := startContextController(t, controllerInventory(t, a.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	contextInventoryFile(t, c, fmt.Sprintf("contexts:\n  - {name: a, url: %s, proxy: none}\n", b.URL))
	controllerUI(c, func() {
		c.active.app.editor.SetInitialText("GET /unsaved")
		c.active.app.editor.SelectRange(len("GET /unsaved"), len("GET /unsaved"))
		c.reload()
		c.openPicker()
		c.choose("named:a")
	})
	waitController(t, c, func() bool { return c.active.profile.Cluster.URL == b.URL })
	controllerUI(c, func() {
		snap := c.active.app.Snapshot()
		if snap.Text != "GET /unsaved" || snap.Cursor != len(snap.Text) {
			t.Error("reconnect discarded live editor", snap)
		}
	})
}
