package wait

import (
	"context"
	"sync"
	"time"
)

type Group struct {
	wg     *sync.WaitGroup
	done   chan struct{}
	closed bool
	lock   *sync.Mutex
}

func NewGroup() *Group {
	return &Group{
		wg:     &sync.WaitGroup{},
		done:   make(chan struct{}),
		closed: false,
		lock:   &sync.Mutex{},
	}
}

func (g *Group) Add(delta int) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if g.closed {
		panic("group already closed")
	}
	g.wg.Add(delta)
}

func (g *Group) Done() {
	g.wg.Done()
}

func (g *Group) Wait() {
	g.wg.Wait()
	g.close()
}

func (g *Group) close() {
	g.lock.Lock()
	defer g.lock.Unlock()
	if !g.closed {
		g.closed = true
		close(g.done)
	}
}

func (g *Group) WaitContext(ctx context.Context) error {
	go g.Wait()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.done:
		return nil
	}
}

func (g *Group) WaitTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return g.WaitContext(ctx)
}
