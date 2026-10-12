package ui

import (
	"context"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
)

type uiJob struct {
	ctx context.Context
	fn  func()
}
type uiDispatcher struct {
	done   <-chan struct{}
	jobs   chan uiJob
	ready  chan struct{}
	once   sync.Once
	mu     sync.RWMutex
	screen tcell.Screen
}

func newUIDispatcher(done <-chan struct{}) *uiDispatcher {
	return &uiDispatcher{done: done, jobs: make(chan uiJob, 64), ready: make(chan struct{})}
}
func (d *uiDispatcher) Post(ctx context.Context, fn func()) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-d.done:
		return false
	default:
	}
	select {
	case d.jobs <- uiJob{ctx, fn}:
	case <-ctx.Done():
		return false
	case <-d.done:
		return false
	}
	select {
	case <-d.ready:
	case <-ctx.Done():
		return false
	case <-d.done:
		return false
	}
	d.mu.RLock()
	screen := d.screen
	d.mu.RUnlock()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-d.done:
			return false
		default:
		}
		if screen.PostEvent(tcell.NewEventKey(tcell.KeyF61, 0, tcell.ModNone)) == nil {
			return true
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-d.done:
			timer.Stop()
			return false
		}
	}
}

// BeforeDraw and Drain run exclusively on tview's UI loop.
func (d *uiDispatcher) BeforeDraw(screen tcell.Screen) bool {
	d.mu.Lock()
	d.screen = screen
	d.mu.Unlock()
	d.once.Do(func() { close(d.ready) })
	d.Drain()
	return false
}
func (d *uiDispatcher) Drain() {
	for {
		select {
		case job := <-d.jobs:
			if job.ctx.Err() != nil {
				continue
			}
			select {
			case <-d.done:
				continue
			default:
			}
			job.fn()
		default:
			return
		}
	}
}

type sessionLease struct {
	identity   string
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	dispatcher *uiDispatcher
	isCurrent  func() bool
}

func newSessionLease(parent context.Context, identity string, generation uint64, dispatcher *uiDispatcher, isCurrent func() bool) *sessionLease {
	ctx, cancel := context.WithCancel(parent)
	return &sessionLease{identity, generation, ctx, cancel, dispatcher, isCurrent}
}
func (s *sessionLease) Context() context.Context { return s.ctx }
func (s *sessionLease) Close()                   { s.cancel() }
func (s *sessionLease) Deliver(fn func()) bool {
	return s.dispatcher.Post(s.ctx, func() {
		if s.ctx.Err() == nil && s.isCurrent() {
			fn()
		}
	})
}
