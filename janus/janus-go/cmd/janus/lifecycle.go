package main

import (
	"context"
	"fmt"
	"time"
)

const (
	// startupTimeout bounds the whole of startup.
	startupTimeout = 90 * time.Second
	// shutdownTimeout bounds shutdown, and the cleanup after a failed startup.
	shutdownTimeout = 10 * time.Second
)

// component is one piece of the running application. A nil start or stop does
// nothing. A component whose start fails must leave nothing running: its stop
// is not called.
type component struct {
	name  string
	start func(ctx context.Context) error
	stop  func(ctx context.Context) error
}

// group is components that belong together, such as the foundation or the web
// side. Groups and the components in them start in the order listed.
type group struct {
	name       string
	components []component
}

// lifecycle starts a table of groups in order and stops what it started in
// reverse order. Adding a component is adding one entry to a group.
type lifecycle struct {
	groups  []group
	started []component
	// startDone, when set, is called with the outcome of the start phase (nil on
	// success) before any rollback, so a span around startup ends first.
	startDone func(err error)
}

func newLifecycle(groups ...group) *lifecycle { return &lifecycle{groups: groups} }

// Start starts every component in order. If one fails, the components already
// started are stopped in reverse order and the error is returned, with any
// failure to stop appended.
func (l *lifecycle) Start(ctx context.Context) error {
	err := l.startAll(ctx)
	if l.startDone != nil {
		l.startDone(err)
	}
	if err != nil {
		return l.rollback(err)
	}
	return nil
}

func (l *lifecycle) startAll(ctx context.Context) error {
	for _, g := range l.groups {
		for _, c := range g.components {
			if c.start != nil {
				if err := c.start(ctx); err != nil {
					return err
				}
			}
			l.started = append(l.started, c)
		}
	}
	return nil
}

func (l *lifecycle) rollback(cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if stopErr := l.Stop(ctx); stopErr != nil {
		return fmt.Errorf("%w; cleanup after startup failure: %v", cause, stopErr)
	}
	return cause
}

// Stop stops the started components in reverse order. It stops every one even
// when some fail, and returns the first error.
func (l *lifecycle) Stop(ctx context.Context) error {
	var first error
	for i := len(l.started) - 1; i >= 0; i-- {
		c := l.started[i]
		if c.stop == nil {
			continue
		}
		if err := c.stop(ctx); err != nil && first == nil {
			first = err
		}
	}
	l.started = nil
	return first
}
