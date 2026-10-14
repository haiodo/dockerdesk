package main

import (
	"context"
	"testing"
	"time"
)

func TestDemoEngine(t *testing.T) {
	e := newDemoEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _ := e.Containers(ctx)
	var run, off *ContainerInfo
	for i := range cs {
		if cs[i].State == "running" && run == nil {
			run = &cs[i]
		}
		if cs[i].State == "exited" && off == nil {
			off = &cs[i]
		}
	}
	if run == nil || off == nil {
		t.Fatal("need running and exited containers")
	}
	var got []Stats
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.StreamStats(ctx, run.ID, func(s Stats) {
			got = append(got, s)
			if len(got) == 2 {
				cancel()
			}
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stats stream stuck")
	}
	if len(got) < 2 || cpuPercent(got[1]) <= 0 || memUsed(got[1]) <= 0 {
		t.Fatalf("bad stats %+v", got)
	}
	if err := e.Action(context.Background(), off.ID, "start"); err != nil {
		t.Fatal(err)
	}
	cs, _ = e.Containers(context.Background())
	for _, c := range cs {
		if c.ID == off.ID && c.State != "running" {
			t.Fatalf("state %s after start", c.State)
		}
	}
	if e.Action(context.Background(), off.ID, "kill") == nil {
		t.Fatal("unknown verb accepted")
	}
}
