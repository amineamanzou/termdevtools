package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Start all simulated applications with the same event barrier and shutdown
// handshake. Stop must run on the UI loop, never race with Run's final cleanup.
func startTestApplication(t *testing.T, tapp *tview.Application, screen tcell.SimulationScreen) {
	t.Helper()
	s := &testSync{tapp: tapp, reached: make(chan struct{}, 64), done: make(chan struct{})}
	capture := tapp.GetInputCapture()
	tapp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyF63:
			tapp.Stop()
			return nil
		case syncKey:
			s.reached <- struct{}{}
			return nil
		}
		if capture != nil {
			return capture(event)
		}
		return event
	})
	testSyncs.Store(screen, s)
	var runErr error
	go func() { runErr = tapp.Run(); close(s.done) }()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			screen.InjectKey(tcell.KeyF63, 0, tcell.ModNone)
		}
		select {
		case <-s.done:
			if runErr != nil {
				t.Errorf("test application: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("test application did not stop")
		}
		testSyncs.Delete(screen)
	})
}

// SimulationScreen.GetContents returns its live slice. Copy the cells and
// their nested slices while the UI loop is idle so later draws cannot race
// with assertions reading the snapshot.
func screenContents(screen tcell.SimulationScreen) ([]tcell.SimCell, int, int) {
	type snapshot struct {
		cells         []tcell.SimCell
		width, height int
	}
	copyContents := func() snapshot {
		cells, width, height := screen.GetContents()
		copied := append([]tcell.SimCell(nil), cells...)
		for i := range copied {
			copied[i].Runes = append([]rune(nil), copied[i].Runes...)
			copied[i].Bytes = append([]byte(nil), copied[i].Bytes...)
		}
		return snapshot{copied, width, height}
	}
	if registered, ok := testSyncs.Load(screen); ok {
		s := registered.(*testSync)
		select {
		case <-s.done:
		default:
			result := make(chan snapshot, 1)
			go s.tapp.QueueUpdate(func() { result <- copyContents() })
			select {
			case view := <-result:
				return view.cells, view.width, view.height
			case <-s.done:
			case <-time.After(5 * time.Second):
				panic("test application did not provide a screen snapshot")
			}
		}
	}
	view := copyContents()
	return view.cells, view.width, view.height
}
