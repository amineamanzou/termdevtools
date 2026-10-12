package ui

import (
	"sort"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/rivo/tview"
)

type contextEntry struct {
	Identity, Name, URL      string
	Labels                   map[string]string
	Active, Changed, Removed bool
}
type contextPicker struct {
	tapp           *tview.Application
	msgs           *i18n.Strings
	entries, shown []contextEntry
	frame          *tview.Flex
	filter         *tview.InputField
	list           *tview.List
	message        *tview.TextView
	selectEntry    func(string)
	cancel         func()
}

func safeContextText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}
func newContextHeader(name, product string) *tview.TextView {
	text := safeContextText(name)
	if product != "" {
		text += " · " + safeContextText(product)
	}
	return tview.NewTextView().SetWrap(false).SetText(text)
}
func newContextPicker(tapp *tview.Application, msgs *i18n.Strings, selectEntry func(string), cancel func()) *contextPicker {
	p := &contextPicker{tapp: tapp, msgs: msgs, selectEntry: selectEntry, cancel: cancel, filter: tview.NewInputField(), list: tview.NewList().ShowSecondaryText(true), message: tview.NewTextView().SetWrap(true)}
	p.filter.SetChangedFunc(func(string) { p.refresh() })
	p.filter.SetInputCapture(p.handleKey)
	p.list.SetInputCapture(p.handleKey)
	p.list.SetSelectedFunc(func(index int, _, _ string, _ rune) { p.selectAt(index) })
	p.frame = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(p.filter, 1, 0, true).AddItem(p.list, 0, 1, false).AddItem(p.message, 2, 0, false)
	p.frame.SetBorder(true)
	p.SetLanguage(msgs)
	return p
}
func (p *contextPicker) Widget() tview.Primitive { return modalPage{p.frame} }
func (p *contextPicker) SetEntries(entries []contextEntry) {
	p.entries = append([]contextEntry(nil), entries...)
	sort.SliceStable(p.entries, func(a, b int) bool { return p.entries[a].Name < p.entries[b].Name })
	p.refresh()
}
func (p *contextPicker) Open()                     { p.filter.SetText(""); p.refresh(); p.FocusFilter() }
func (p *contextPicker) FocusFilter()              { p.tapp.SetFocus(p.filter) }
func (p *contextPicker) SetMessage(message string) { p.message.SetText(safeContextText(message)) }
func (p *contextPicker) SetLanguage(msgs *i18n.Strings) {
	p.msgs = msgs
	p.filter.SetLabel(msgs.ContextFilterLabel)
	p.frame.SetTitle(msgs.ContextPickerTitle)
	p.refresh()
}
func (p *contextPicker) refresh() {
	words := strings.Fields(strings.ToLower(p.filter.GetText()))
	p.shown = nil
	p.list.Clear()
	for _, entry := range p.entries {
		keys := make([]string, 0, len(entry.Labels))
		for key := range entry.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		labels := make([]string, 0, len(keys))
		for _, key := range keys {
			labels = append(labels, key+"="+entry.Labels[key])
		}
		joined := strings.Join(labels, " ")
		haystack := strings.ToLower(entry.Name + " " + entry.URL + " " + joined)
		matched := true
		for _, word := range words {
			if !strings.Contains(haystack, word) {
				matched = false
				break
			}
		}
		if !matched && !(entry.Active && entry.Removed) {
			continue
		}
		p.shown = append(p.shown, entry)
		label := entry.Name
		if entry.Active {
			label += " " + p.msgs.ContextActiveMark
		}
		if entry.Changed {
			label += " " + p.msgs.ContextChangedMark
		}
		if entry.Removed {
			label += " " + p.msgs.ContextRemovedMark
		}
		p.list.AddItem(tview.Escape(safeContextText(label)), tview.Escape(safeContextText(joined+" · "+entry.URL)), 0, nil)
	}
	if len(p.shown) == 0 {
		p.SetMessage(p.msgs.ContextsNoMatch)
	} else {
		p.SetMessage("")
		p.list.SetCurrentItem(0)
	}
}
func (p *contextPicker) selectAt(index int) {
	if index >= 0 && index < len(p.shown) && !p.shown[index].Removed {
		p.selectEntry(p.shown[index].Identity)
	}
}
func (p *contextPicker) handleKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		p.cancel()
		return nil
	case tcell.KeyEnter:
		p.selectAt(p.list.GetCurrentItem())
		return nil
	case tcell.KeyUp:
		if index := p.list.GetCurrentItem(); index > 0 {
			p.list.SetCurrentItem(index - 1)
		}
		return nil
	case tcell.KeyDown:
		if index := p.list.GetCurrentItem(); index+1 < len(p.shown) {
			p.list.SetCurrentItem(index + 1)
		}
		return nil
	}
	return event
}
