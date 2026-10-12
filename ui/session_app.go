package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/config"
	"github.com/rivo/tview"
)

type editorSnapshot struct {
	Text   string
	Cursor int
}
type workspace struct {
	Files             config.ContextFiles
	Text, SavedText   string
	Cursor            int
	Variables         map[string]string
	QueriesLoadFailed bool
}

var contextVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseContextVariables(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !contextVariableName.MatchString(name) {
			return nil, fmt.Errorf("line %d: expected name=value", n+1)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("line %d: duplicate variable name", n+1)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}
func prepareWorkspace(files config.ContextFiles, starter string, snapshot *editorSnapshot) (workspace, error) {
	work := workspace{Files: files, Text: starter, SavedText: starter, Variables: map[string]string{}}
	data, err := os.ReadFile(files.Queries)
	if err != nil && !os.IsNotExist(err) {
		return workspace{}, fmt.Errorf("%s: %w", files.Queries, err)
	}
	if err == nil {
		work.Text = string(data)
		work.SavedText = work.Text
	}
	data, err = os.ReadFile(files.Variables)
	if err != nil && !os.IsNotExist(err) {
		return workspace{}, fmt.Errorf("%s: %w", files.Variables, err)
	}
	if err == nil {
		vars, e := parseContextVariables(data)
		if e != nil {
			return workspace{}, fmt.Errorf("%s: %w", files.Variables, e)
		}
		work.Variables = vars
	}
	if snapshot != nil {
		work.Text = snapshot.Text
		work.Cursor = snapshot.Cursor
	}
	if work.Cursor < 0 {
		work.Cursor = 0
	}
	if work.Cursor > len(work.Text) {
		work.Cursor = len(work.Text)
	}
	for work.Cursor > 0 && work.Cursor < len(work.Text) && !utf8.RuneStart(work.Text[work.Cursor]) {
		work.Cursor--
	}
	return work, nil
}
func newSessionApp(tapp *tview.Application, cr ConnectResult, cfg *config.Config, paths Paths, work workspace, lease *sessionLease) *App {
	a := newApp(tapp, cr, cfg, paths, &work)
	a.lease = lease
	a.namedContext = true
	return a
}
func (a *App) Snapshot() editorSnapshot {
	return editorSnapshot{a.editor.Text(), a.editor.CursorOffset()}
}
func (a *App) SaveBeforeSwitch() error {
	text := a.editor.Text()
	if text == a.savedText {
		return nil
	}
	if a.queriesLoadFailed {
		return errors.New(a.msgs.ErrSavedRequestsUnread)
	}
	if a.queriesPath == "" {
		return nil
	}
	if err := a.editor.SaveToFile(a.queriesPath); err != nil {
		return err
	}
	a.savedText = text
	return nil
}
func (a *App) BindScreen(screen tcell.Screen) { a.screen = screen }
func (a *App) operationContext() context.Context {
	if a.lease != nil {
		return a.lease.Context()
	}
	return context.Background()
}
func (a *App) deliver(fn func()) {
	if a.lease != nil {
		a.lease.Deliver(fn)
	} else {
		a.tapp.QueueUpdateDraw(fn)
	}
}

func prepareLegacyWorkspace(files config.ContextFiles, cheatsheet, starter string, snapshot *editorSnapshot) (workspace, error) {
	work := workspace{Files: files, Text: starter, SavedText: starter}
	data, err := os.ReadFile(files.Queries)
	if err == nil {
		work.Text = string(data)
	} else if !os.IsNotExist(err) {
		work.Text = ""
		work.QueriesLoadFailed = true
	} else if cheatsheet != "" {
		data, e := os.ReadFile(cheatsheet)
		if e == nil {
			work.Text = string(data)
		}
	}
	work.SavedText = work.Text
	if snapshot != nil {
		work.Text = snapshot.Text
		work.Cursor = snapshot.Cursor
	}
	if work.Cursor < 0 {
		work.Cursor = 0
	}
	if work.Cursor > len(work.Text) {
		work.Cursor = len(work.Text)
	}
	for work.Cursor > 0 && work.Cursor < len(work.Text) && !utf8.RuneStart(work.Text[work.Cursor]) {
		work.Cursor--
	}
	work.Variables, err = LoadVariablesFile(files.Variables)
	if err != nil {
		return workspace{}, fmt.Errorf("%s: %w", files.Variables, err)
	}
	return work, nil
}
