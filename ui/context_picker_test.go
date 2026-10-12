package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/rivo/tview"
)

func TestContextPickerFilter(t *testing.T) {
	p := newContextPicker(tview.NewApplication(), i18n.For(i18n.EN), func(string) {}, func() {})
	p.SetEntries([]contextEntry{{Identity: "named:z", Name: "z", URL: "http://z", Labels: map[string]string{"env": "prod", "client": "acme"}}, {Identity: "named:a", Name: "a", URL: "http://a", Labels: map[string]string{"env": "dev"}}, {Identity: "named:b", Name: "b", URL: "http://b", Labels: map[string]string{"client": "équipe [red]\x1b\n"}}})
	if len(p.shown) != 3 || p.shown[0].Name != "a" {
		t.Fatal(p.shown)
	}
	p.filter.SetText("PROD client=acme")
	if len(p.shown) != 1 || p.shown[0].Name != "z" {
		t.Fatal(p.shown)
	}
	p.filter.SetText("équipe [red]")
	if len(p.shown) != 1 || p.shown[0].Name != "b" {
		t.Fatal(p.shown)
	}
	text, _ := p.list.GetItemText(0)
	if strings.ContainsAny(text, "\x1b\n") {
		t.Fatal("raw control in row")
	}
}
func TestContextPickerEmptyEnter(t *testing.T) {
	count := 0
	p := newContextPicker(tview.NewApplication(), i18n.For(i18n.EN), func(string) { count++ }, func() {})
	p.SetEntries([]contextEntry{{Identity: "named:a", Name: "a"}})
	p.filter.SetText("no match")
	p.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if count != 0 {
		t.Fatal("selected stale entry")
	}
	p.SetEntries([]contextEntry{{Identity: "named:a", Name: "a", Removed: true}})
	p.filter.SetText("")
	p.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if count != 0 {
		t.Fatal("selected removed entry")
	}
}
func TestContextPickerFocus(t *testing.T) {
	app := tview.NewApplication()
	p := newContextPicker(app, i18n.For(i18n.EN), func(string) {}, func() {})
	p.Open()
	if app.GetFocus() != p.filter {
		t.Fatal("filter not focused")
	}
	app.SetFocus(p.list)
	p.FocusFilter()
	if app.GetFocus() != p.filter {
		t.Fatal("F6 did not focus filter")
	}
}
func TestContextHeaderAt80Columns(t *testing.T) {
	for _, lang := range []string{i18n.EN, i18n.FR} {
		t.Run(lang, func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			defer s.Fini()
			s.SetSize(80, 24)
			name := strings.Repeat("a", 64)
			header := newContextHeader(name, "OpenSearch 3.9.0")
			header.SetRect(0, 0, 80, 1)
			header.Draw(s)
			s.Show()
			cells, w, _ := s.GetContents()
			var row strings.Builder
			for _, c := range cells[:w] {
				for _, r := range c.Runes {
					row.WriteRune(r)
				}
			}
			if !strings.Contains(row.String(), name) {
				t.Fatal(row.String())
			}
		})
	}
}
func BenchmarkContextPickerFilter500(b *testing.B) {
	p := newContextPicker(tview.NewApplication(), i18n.For(i18n.EN), func(string) {}, func() {})
	entries := make([]contextEntry, 500)
	for i := range entries {
		entries[i] = contextEntry{Identity: fmt.Sprintf("named:%d", i), Name: fmt.Sprintf("prod-%03d", i), URL: "https://example.net", Labels: map[string]string{"client": "acme", "region": "eu"}}
	}
	p.SetEntries(entries)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.filter.SetText(fmt.Sprintf("client=acme %d", i%10))
	}
}
