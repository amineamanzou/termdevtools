package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"termdevtools/refdata"
)

func TestCompletionCombinedRecovery(t *testing.T) {
	for _, target := range []struct {
		name   string
		target refdata.Target
	}{
		{"Elasticsearch", es95},
		{"OpenSearch", refdata.Target{Distribution: refdata.OpenSearch, Version: refdata.Version{Major: 3, Minor: 9}, HasVersion: true}},
	} {
		for _, mouse := range []bool{false, true} {
			name := target.name + "/keyboard"
			if mouse {
				name = target.name + "/mouse"
			}
			t.Run(name, func(t *testing.T) {
				app, screen := newTestAppWith(t, testAppOptions{target: target.target, mouse: mouse})
				const before = "# résumé\nGET /_cat/s?format=json&v=false\nGET _cluster/health"
				onUI(app, func() {
					app.editor.view.SetText(before, false)
					cursor := len("# résumé\nGET /_cat/s")
					app.editor.view.Select(cursor, cursor)
				})
				screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
				injectText(screen, "hzz")
				screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
				waitForDraw(t, screen)
				if mouse {
					var x, y int
					onUI(app, func() { x, y, _, _ = app.completionList.GetInnerRect() })
					click(screen, x+1, y)
					waitForDraw(t, screen)
				}
				if got := editorText(app); got != before {
					t.Fatalf("an unmatched search changed the request: %q", got)
				}
				if !uiValue(app, func() bool {
					_, _, _, height := app.completionList.GetRect()
					return height > 0 && app.tapp.GetFocus() == app.completionList
				}) {
					t.Fatal("an unmatched search must leave the popup visible and focused")
				}
				screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
				screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
				screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
				waitForDraw(t, screen)
				const want = "# résumé\nGET /_cat/shards?format=json&v=false\nGET _cluster/health"
				if got := editorText(app); got != want {
					t.Fatalf("recovery must preserve the slash, user query and surrounding text: got %q, want %q", got, want)
				}
			})
		}
	}
}

func TestCompletionNoMatchReset(t *testing.T) {
	for _, reset := range []string{"backspace", "reopen"} {
		t.Run(reset, func(t *testing.T) {
			app, screen := newTestApp(t)
			injectText(screen, "GET /_cat/s")
			screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
			injectText(screen, "z")
			if reset == "backspace" {
				screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
			} else {
				screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
				screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
			}
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			waitForDraw(t, screen)
			if got := editorText(app); got != "GET /_cat/segments?v" {
				t.Fatalf("reset must allow a normal selection, got %q", got)
			}
			if strings.Contains(statusText(app), app.msgs.ErrNoCompletion) {
				t.Fatal("reset left a stale no-match error")
			}
		})
	}
}

func TestCompletionCustomQueryOverridesRepeatedDefaults(t *testing.T) {
	userDir := t.TempDir()
	writeReferenceFile(t, userDir, "endpoints.txt", "/_custom/status?pretty=true&tag=a&tag=b&x%20y=default\n")
	app, screen := newTestAppWith(t, testAppOptions{reference: refdata.Sources{UserDir: userDir}})
	onUI(app, func() {
		app.editor.view.SetText("GET /_custom/st?pretty=false&tag=c%2Bd&tag=e+f&x+y=override", false)
		cursor := len("GET /_custom/st")
		app.editor.view.Select(cursor, cursor)
	})
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitForDraw(t, screen)
	const want = "GET /_custom/status?pretty=false&tag=c%2Bd&tag=e+f&x+y=override"
	if got := editorText(app); got != want {
		t.Fatalf("user query must override every equivalent default without re-encoding values: got %q, want %q", got, want)
	}
}

func TestCatCompletionRecoveryPreservesOtherColumnsAndParameters(t *testing.T) {
	app, screen := newTestApp(t)
	const before = "GET /_cat/indices?h=health,s&format=json"
	onUI(app, func() {
		app.editor.view.SetText(before, false)
		cursor := len("GET /_cat/indices?h=health,s")
		app.editor.view.Select(cursor, cursor)
	})
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	injectText(screen, "tzz")
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForDraw(t, screen)
	if got := editorText(app); got != before {
		t.Fatalf("an unmatched CAT column search changed the request: %q", got)
	}
	screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForDraw(t, screen)
	if got, want := editorText(app), "GET /_cat/indices?h=health,status&format=json"; got != want {
		t.Fatalf("column completion changed surrounding parameters: got %q, want %q", got, want)
	}
}
