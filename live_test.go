package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveReadOnly runs only with DOCKERDESK_LIVE=1 and calls read-only endpoints of the real engine.
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("DOCKERDESK_LIVE") != "1" {
		t.Skip("set DOCKERDESK_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, info, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("engine %s, %d CPUs, mem %s", info.ServerVersion, info.NCPU, formatBytes(info.MemTotal))
	p := newPoller(e)
	s := p.fetch(ctx)
	time.Sleep(3 * time.Second) // let the stats streams deliver their frames
	s = p.fetch(ctx)
	t.Logf("open streams: %d", p.openStreams())
	cancel()
	ctx = context.Background()
	if s.err != nil {
		t.Fatal(s.err)
	}
	cpu, mem := sumUsage(s.ctrs)
	t.Logf("%d containers, total cpu %.2f%%, mem %s, %d groups", len(s.ctrs), cpu, formatBytes(mem), len(groupContainers(s.ctrs)))
	if len(s.ctrs) > 0 {
		n := 0
		err := e.Logs(ctx, s.ctrs[0].ID, 5, false, func(byte, string) { n++ })
		t.Logf("log lines (tail 5, no follow): %d, err %v", n, err)
	}
	imgs, ierr := e.Images(ctx)
	vols, verr := e.Volumes(ctx)
	t.Logf("images %d (%v), volumes %d (%v)", len(imgs), ierr, len(vols), verr)
}
