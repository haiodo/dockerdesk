package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeEngine serves a changeable list and holds a stats stream open until its context ends.
type fakeEngine struct {
	Engine
	mu     sync.Mutex
	list   []ContainerInfo
	err    error
	opened map[string]int
	open   int
}

func (f *fakeEngine) set(list ...ContainerInfo) {
	f.mu.Lock()
	f.list = list
	f.mu.Unlock()
}

func (f *fakeEngine) Containers(context.Context) ([]ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ContainerInfo(nil), f.list...), f.err
}

func (f *fakeEngine) StartedAt(context.Context, string) (time.Time, error) {
	return time.Unix(1700000000, 0), nil
}

func (f *fakeEngine) StreamStats(ctx context.Context, id string, onFrame func(Stats)) error {
	f.mu.Lock()
	f.open++
	if f.opened == nil {
		f.opened = map[string]int{}
	}
	f.opened[id]++
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.open--; f.mu.Unlock() }()
	onFrame(Stats{CPUTotal: 200, PreCPUTotal: 100, CPUSystem: 2000, PreCPUSystem: 1000, OnlineCPUs: 2, MemUsage: 10})
	<-ctx.Done()
	return nil
}

func (f *fakeEngine) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func TestStreamLifecycle(t *testing.T) {
	a := mkCtr("a", "up", "", "running").ContainerInfo
	b := mkCtr("b", "up2", "", "running").ContainerInfo
	off := mkCtr("c", "down", "", "exited").ContainerInfo
	f := &fakeEngine{}
	f.set(a, b, off)
	ctx, cancel := context.WithCancel(context.Background())
	p := newPoller(f)

	s := p.fetch(ctx)
	if len(s.ctrs) != 3 {
		t.Fatalf("list %d", len(s.ctrs))
	}
	waitFor(t, "two streams for the running containers", func() bool { return f.openCount() == 2 && p.openStreams() == 2 })
	if f.opened[off.ID] != 0 {
		t.Fatal("no stream for a stopped container")
	}
	waitFor(t, "first frames", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.samples) == 2 })
	s = p.fetch(ctx)
	if s.ctrs[0].CPU != 20 || s.ctrs[0].MemUsed != 10 || s.ctrs[0].NoStats || s.ctrs[2].CPU != 0 {
		t.Fatalf("%+v", s.ctrs[0])
	}

	a.State = "exited"
	f.set(a, b, off)
	p.fetch(ctx)
	waitFor(t, "stream closed when a stops", func() bool { return f.openCount() == 1 })

	f.set(off)
	p.fetch(ctx)
	waitFor(t, "stream closed when b disappears", func() bool { return f.openCount() == 0 && p.openStreams() == 0 })

	f.set(mkCtr("a", "up", "", "running").ContainerInfo)
	p.fetch(ctx)
	waitFor(t, "stream reopened when a runs again", func() bool { return f.openCount() == 1 })
	cancel()
	waitFor(t, "all streams closed on cancel", func() bool { return f.openCount() == 0 })
}

func TestFetchError(t *testing.T) {
	f := &fakeEngine{err: errors.New("down")}
	if s := newPoller(f).fetch(context.Background()); s.err == nil {
		t.Fatal("error must be reported")
	}
}
