package ui

import (
	"github.com/gdamore/tcell/v2"
	"testing"
)

func TestPickerCancelDoesNotRestoreHiddenCompletion(t *testing.T) {
	srv := clusterServer(t)
	c, s := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() {
		a := c.active.app
		a.editor.SetInitialText("GET _cat/")
		a.editor.SelectRange(len("GET _cat/"), len("GET _cat/"))
		a.openCompletion(4, len("GET _cat/"), len("GET _cat/"), []string{"_cat/indices", "_cat/shards"})
	})
	s.InjectKey(tcell.KeyF6, 0, 0)
	s.InjectKey(tcell.KeyEscape, 0, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		a := c.active.app
		_, _, _, height := a.completionList.GetRect()
		if a.completionList.HasFocus() && height == 0 {
			t.Error("restored focus to invisible completion popup")
		}
	})
	s.InjectKey(tcell.KeyEnter, 0, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if got := c.active.app.editor.Text(); got != "GET _cat/\n" {
			t.Errorf("Enter applied an invisible completion: %q", got)
		}
	})
}

func TestPickerMessageMouseKeepsEscapeWorking(t *testing.T) {
	srv := clusterServer(t)
	c, s := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() { c.options.App.EnableMouse(true); c.openPicker(); c.picker.SetMessage("connection status") })
	waitForDraw(t, s)
	var x, y int
	controllerUI(c, func() { x, y, _, _ = c.picker.message.GetInnerRect() })
	s.InjectMouse(x, y, tcell.Button1, 0)
	s.InjectMouse(x, y, tcell.ButtonNone, 0)
	s.InjectKey(tcell.KeyDown, 0, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if c.picker.list.GetCurrentItem() != 1 {
			t.Error("status focus blocked picker navigation")
		}
	})
	s.InjectKey(tcell.KeyEscape, 0, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if c.pickerOpen {
			t.Error("Escape does not close picker after clicking status")
		}
	})
}
func TestLegacyListMouseDoesNotReachHiddenEditor(t *testing.T) {
	srv := clusterServer(t)
	c, s := startContextController(t, controllerInventory(t, srv.URL))
	controllerUI(c, func() { c.choose("named:a") })
	waitController(t, c, func() bool { return c.active != nil })
	controllerUI(c, func() { c.options.App.EnableMouse(true); c.openLegacy() })
	waitForDraw(t, s)
	s.InjectMouse(6, 15, tcell.Button1, 0)
	s.InjectMouse(6, 15, tcell.ButtonNone, 0)
	waitForDraw(t, s)
	controllerUI(c, func() {
		if c.active.app.editor.Primitive().HasFocus() {
			t.Error("click focuses editor behind legacy list")
		}
	})
}
