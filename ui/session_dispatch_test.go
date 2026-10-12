package ui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestSessionLeaseDropsOldResults(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	d := newUIDispatcher(done)
	screen := tcell.NewSimulationScreen("")
	screen.Init()
	defer screen.Fini()
	d.BeforeDraw(screen)
	current := true
	lease := newSessionLease(context.Background(), "named:a", 1, d, func() bool { return current })
	applied := false
	if !lease.Deliver(func() { applied = true }) {
		t.Fatal("not published")
	}
	lease.Close()
	d.Drain()
	if applied {
		t.Fatal("old lease delivered")
	}
	next := newSessionLease(context.Background(), "named:a", 2, d, func() bool { return current })
	if !next.Deliver(func() { applied = true }) {
		t.Fatal("not published")
	}
	d.Drain()
	if !applied {
		t.Fatal("new lease lost")
	}
	next.Close()
}

type observedEventScreen struct {
	tcell.Screen
	attempted chan struct{}
}

func (s observedEventScreen) PostEvent(event tcell.Event) error {
	select {
	case s.attempted <- struct{}{}:
	default:
	}
	return s.Screen.PostEvent(event)
}

func TestDispatcherShutdown(t *testing.T) {
	t.Run("already closed", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		d := newUIDispatcher(done)
		if d.Post(context.Background(), func() { t.Error("after shutdown") }) {
			t.Fatal("accepted after shutdown")
		}
	})
	t.Run("before screen", func(t *testing.T) {
		done := make(chan struct{})
		d := newUIDispatcher(done)
		result := make(chan bool, 1)
		go func() { result <- d.Post(context.Background(), func() { t.Error("after shutdown") }) }()
		close(done)
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("publisher blocked")
		}
	})
	t.Run("full screen queue", func(t *testing.T) {
		done := make(chan struct{})
		d := newUIDispatcher(done)
		s := tcell.NewSimulationScreen("")
		s.Init()
		defer s.Fini()
		attempted := make(chan struct{}, 1)
		d.BeforeDraw(observedEventScreen{s, attempted})
		for s.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0)) == nil {
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan bool, 1)
		go func() { result <- d.Post(ctx, func() { t.Error("cancelled callback") }) }()
		select {
		case <-attempted:
		case <-time.After(time.Second):
			t.Fatal("no wake attempted")
		}
		cancel()
		close(done)
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("publisher blocked on full queue")
		}
		d.Drain()
	})
}
