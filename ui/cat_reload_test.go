package ui

import (
	"errors"
	"strings"
	"testing"

	"termdevtools/esclient"
)

func TestCatReloadIgnoresEarlierResponses(t *testing.T) {
	for _, oldFirst := range []bool{true, false} {
		name := "old_after_new"
		if oldFirst {
			name = "old_before_new"
		}
		t.Run(name, func(t *testing.T) {
			app, _ := newTestApp(t)
			onUI(app, func() {
				app.catPending["indices"] = true
				app.status.SetRunning()
				oldResponse := app.catColumnsResponse("indices")
				app.reloadUserFiles()
				app.catPending["indices"] = true
				app.status.SetRunning()
				newResponse := app.catColumnsResponse("indices")
				newResult := &esclient.Result{StatusCode: 200, Body: []byte("new.column | n | new column\n")}
				oldResult := &esclient.Result{StatusCode: 200, Body: []byte("old.column | o | old column\n")}
				if !oldFirst {
					newResponse(newResult, nil)
				}
				status, text := app.status.Version(), app.editor.Text()
				oldResponse(oldResult, nil)
				if app.status.Version() != status || app.editor.Text() != text {
					t.Error("an old response changed status or editor after reload")
				}
				if oldFirst {
					if !app.catPending["indices"] {
						t.Error("old response cleared the new request's pending marker")
					}
					if _, exists := app.catLive["indices"]; exists {
						t.Error("old response populated the reloaded cache")
					}
					newResponse(newResult, nil)
				}
				if got := strings.Join(app.catLive["indices"], ","); got != "new.column" {
					t.Errorf("reloaded cache contains %q, want new.column", got)
				}
				// A failed old request must not restore the built-in fallback either.
				oldResponse(nil, errors.New("old timeout"))
				if got := strings.Join(app.catLive["indices"], ","); got != "new.column" {
					t.Errorf("old failure replaced new columns with %q", got)
				}
			})
		})
	}
}
