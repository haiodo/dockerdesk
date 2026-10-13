package main

import (
	"context"
	"log"
	"runtime"
	"sync"
	"time"
)

const (
	pollEvery     = 1500 * time.Millisecond
	inspectWorker = 8
)

// snapshot is one poll result handed to the UI thread.
type snapshot struct {
	ctrs []*Ctr
	err  error
}

type startedEntry struct {
	state string
	at    time.Time
}

type sample struct {
	cpu        float64
	mem, limit int64
}

type streamEntry struct{ cancel context.CancelFunc }

// poller reads the engine off the UI thread. The container list comes from one call per cycle and
// is posted at once; CPU and memory come from one open stats stream per running container, merged
// into the snapshot at post time, so a cycle never waits for them.
type poller struct {
	eng   Engine
	kick  chan struct{}
	trace bool // log one line per second with the stream count
	sem   chan struct{}

	mu       sync.Mutex
	started  map[string]startedEntry // start time per container and state; a restart that keeps "running" is seen after the next state change
	inflight map[string]bool         // inspects in progress
	streams  map[string]*streamEntry
	samples  map[string]sample
}

func newPoller(eng Engine) *poller {
	return &poller{
		eng: eng, kick: make(chan struct{}, 1), sem: make(chan struct{}, inspectWorker),
		started: map[string]startedEntry{}, inflight: map[string]bool{},
		streams: map[string]*streamEntry{}, samples: map[string]sample{},
	}
}

// Refresh asks for a poll right away, e.g. after an action on ids.
func (p *poller) Refresh(ids ...string) {
	p.mu.Lock()
	for _, id := range ids {
		delete(p.started, id)
	}
	p.mu.Unlock()
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Run polls until ctx ends and calls post with every result.
func (p *poller) Run(ctx context.Context, post func(snapshot)) {
	if p.trace {
		go p.traceLoop(ctx)
	}
	for ctx.Err() == nil {
		t0 := time.Now()
		snap := p.fetch(ctx)
		if p.trace {
			log.Printf("poll %d containers in %s", len(snap.ctrs), time.Since(t0).Round(time.Millisecond))
		}
		post(snap)
		select {
		case <-ctx.Done():
		case <-p.kick:
		case <-time.After(max(0, pollEvery-time.Since(t0))):
		}
	}
}

func (p *poller) traceLoop(ctx context.Context) {
	for t := time.NewTicker(time.Second); ; {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			log.Printf("streams=%d goroutines=%d", p.openStreams(), runtime.NumGoroutine())
		}
	}
}

func (p *poller) openStreams() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.streams)
}

func (p *poller) fetch(ctx context.Context) snapshot {
	infos, err := p.eng.Containers(ctx)
	if err != nil {
		return snapshot{err: err}
	}
	ctrs := make([]*Ctr, len(infos))
	for i := range infos {
		ctrs[i] = &Ctr{ContainerInfo: infos[i]}
	}
	p.syncStreams(ctx, ctrs)
	p.mu.Lock()
	defer p.mu.Unlock()
	live := make(map[string]bool, len(ctrs))
	for _, c := range ctrs {
		live[c.ID] = true
		c.Started = p.startedLocked(ctx, c)
		if s, ok := p.samples[c.ID]; ok && c.Running() {
			c.CPU, c.MemUsed, c.MemLimit = s.cpu, s.mem, s.limit
		} else if c.Running() {
			c.NoStats = true
		}
	}
	for id := range p.started {
		if !live[id] {
			delete(p.started, id)
		}
	}
	return snapshot{ctrs: ctrs}
}

// startedLocked returns the cached start time and refreshes it in the background when the state changed.
func (p *poller) startedLocked(ctx context.Context, c *Ctr) time.Time {
	e, ok := p.started[c.ID]
	if (!ok || e.state != c.State) && !p.inflight[c.ID] {
		p.inflight[c.ID] = true
		go p.inspect(ctx, c.ID, c.State)
	}
	return e.at
}

func (p *poller) inspect(ctx context.Context, id, state string) {
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	at, err := p.eng.StartedAt(ctx, id)
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, id)
	if err == nil {
		p.started[id] = startedEntry{state, at}
	}
}

// syncStreams opens a stats stream for each running container that has none and closes the
// streams of containers that stopped or disappeared.
func (p *poller) syncStreams(ctx context.Context, ctrs []*Ctr) {
	running := make(map[string]bool, len(ctrs))
	for _, c := range ctrs {
		if c.Running() {
			running[c.ID] = true
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.streams {
		if !running[id] {
			e.cancel()
			delete(p.streams, id)
			delete(p.samples, id)
		}
	}
	for id := range running {
		if _, ok := p.streams[id]; !ok && ctx.Err() == nil {
			p.openStreamLocked(ctx, id)
		}
	}
}

func (p *poller) openStreamLocked(ctx context.Context, id string) {
	sctx, cancel := context.WithCancel(ctx)
	e := &streamEntry{cancel}
	p.streams[id] = e
	go func() {
		defer cancel()
		_ = p.eng.StreamStats(sctx, id, func(s Stats) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.streams[id] == e {
				p.samples[id] = sample{cpuPercent(s), memUsed(s), int64(s.MemLimit)}
			}
		})
		// A stream that ended on its own (daemon error, container exit) is reopened by the next cycle if the container still runs.
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.streams[id] == e {
			delete(p.streams, id)
			delete(p.samples, id)
		}
	}()
}
