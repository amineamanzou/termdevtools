package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gnocent/termdevtools/parser"
)

func TestExecuteRejectsInvalidCursorLine(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	app, _ := newTestAppWith(t, testAppOptions{clusterURL: srv.URL})
	onUI(app, func() {
		app.editor.view.SetText("DELETE /index\n\nGET", false)
		cursor := len(app.editor.Text())
		app.editor.view.Select(cursor, cursor)
		app.executeCurrent()
	})
	if got := statusText(app); !strings.Contains(got, parser.ErrNoRequest.Error()) {
		t.Fatalf("invalid cursor line must report an error before sending anything, got %q", got)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("invalid cursor line sent %d requests", got)
	}
	onUI(app, func() {
		app.editor.view.SetText("GET /_cluster/health", false)
		app.executeCurrent()
	})
	eventually(t, "a valid request after the invalid line", func() bool {
		return strings.Contains(uiValue(app, app.result.PlainText), "# GET /_cluster/health")
	})
	if got := hits.Load(); got != 1 {
		t.Fatalf("only the valid request should reach the server, got %d requests", got)
	}
}
