// Command dockerdesk is a small Docker Desktop look-alike written on the gowt facade: containers
// and compose groups with live CPU and memory, start/stop/restart, a CPU chart and a log viewer.
// It talks to the Docker Engine through the official Docker SDK (github.com/moby/moby/client).
//
// Run:
//
//	go run .          # DOCKER_HOST, else /var/run/docker.sock, else ~/.docker/run/docker.sock
//	go run . -mem     # also log Go heap statistics to stderr every 10 s
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/haiodo/gowt"
)

func main() {
	mem := flag.Bool("mem", false, "log Go runtime memory statistics to stderr every 10 s")
	flag.Parse()
	// A soft limit keeps collections rare without the heap growing past it; a percent-based setting let the runtime take ~70 MB.
	debug.SetMemoryLimit(32 << 20)
	ab.noApply, ab.noTree, ab.noLabels = os.Getenv("DOCKERDESK_NOAPPLY") == "1", os.Getenv("DOCKERDESK_NOTREE") == "1", os.Getenv("DOCKERDESK_NOLABELS") == "1"
	if *mem {
		go logMemory(10 * time.Second)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	eng, info, err := connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	err = gowt.Run(func(app *gowt.App) {
		u := &ui{app: app, eng: eng, info: info, ctx: ctx, stop: stop, poll: newPoller(eng), trace: *mem}
		u.poll.trace = *mem
		u.build()
		u.flushLogs()
		go u.poll.Run(ctx, func(s snapshot) { app.Async(func() { u.apply(s) }) })
	})
	if err != nil {
		log.Fatal(err)
	}
}

func connect(ctx context.Context) (Engine, Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var eng Engine
	var err error
	if os.Getenv("DOCKERDESK_DEMO") == "1" {
		eng = newDemoEngine()
	} else {
		eng, err = newEngine()
	}
	if err != nil {
		return nil, Info{}, err
	}
	info, err := eng.Info(ctx)
	return eng, info, err
}

// logMemory prints Go-side numbers; the process RSS is measured from outside.
func logMemory(every time.Duration) {
	var m runtime.MemStats
	for range time.Tick(every) {
		runtime.ReadMemStats(&m)
		log.Printf("mem heapAlloc=%dKiB heapSys=%dKiB sys=%dKiB gc=%d goroutines=%d",
			m.HeapAlloc>>10, m.HeapSys>>10, m.Sys>>10, m.NumGC, runtime.NumGoroutine())
	}
}

// ab holds the A/B switches for hunting memory spikes: each one skips part of what a poll result does to the UI.
var ab struct{ noApply, noTree, noLabels bool }
