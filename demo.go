package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// demoEngine is a static fake daemon for README screenshots (DOCKERDESK_DEMO=1); it never touches a socket.
type demoEngine struct {
	mu    sync.Mutex
	ctrs  []ContainerInfo
	start time.Time
	boot  time.Time
}

type demoSpec struct {
	group, name, image, state, status string
	ports                             []Port
	cpu, mem                          float64 // mean CPU percent and memory in MiB while running
}

var demoSpecs = []demoSpec{
	{"shop", "shop-web-1", "nginx:1.27-alpine", "running", "Up 3 hours", []Port{{8080, 80}, {8443, 443}}, 6, 18},
	{"shop", "shop-api-1", "ghcr.io/acme/shop-api:2.4.1", "running", "Up 3 hours", []Port{{3000, 3000}}, 38, 212},
	{"shop", "shop-postgres-1", "postgres:16", "running", "Up 3 hours (healthy)", []Port{{5432, 5432}}, 14, 164},
	{"shop", "shop-redis-1", "redis:7-alpine", "running", "Up 3 hours", []Port{{6379, 6379}}, 3, 9},
	{"monitoring", "monitoring-prometheus-1", "prom/prometheus:v2.54.1", "running", "Up 2 days", []Port{{9090, 9090}}, 22, 148},
	{"monitoring", "monitoring-grafana-1", "grafana/grafana:11.2.0", "exited", "Exited (0) 5 hours ago", nil, 0, 0},
	{"", "mailpit", "axllent/mailpit:v1.20", "running", "Up 6 days", []Port{{8025, 8025}, {1025, 1025}}, 2, 24},
	{"", "minio", "minio/minio:latest", "running", "Up 6 days", []Port{{9000, 9000}, {9001, 9001}}, 9, 96},
	{"", "migrate-job", "ghcr.io/acme/shop-api:2.4.1", "exited", "Exited (1) 2 days ago", nil, 0, 0},
}

func newDemoEngine() *demoEngine {
	now := time.Now()
	e := &demoEngine{start: now.Add(-3 * time.Hour), boot: now}
	for i, s := range demoSpecs {
		c := ContainerInfo{ID: fmt.Sprintf("%012x%052x", 0xd0c0a0000000+i, i), Names: []string{"/" + s.name}, Image: s.image, State: s.state, Status: s.status, Ports: s.ports, Labels: map[string]string{}}
		if s.group != "" {
			c.Labels[composeLabel] = s.group
		}
		e.ctrs = append(e.ctrs, c)
	}
	return e
}

func (e *demoEngine) spec(id string) (demoSpec, int) {
	for i, c := range e.ctrs {
		if c.ID == id {
			return demoSpecs[i], i
		}
	}
	return demoSpec{}, -1
}

func (e *demoEngine) Info(context.Context) (Info, error) {
	return Info{NCPU: 8, MemTotal: 16 << 30, ServerVersion: "27.3.1"}, nil
}

func (e *demoEngine) Containers(context.Context) ([]ContainerInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ContainerInfo(nil), e.ctrs...), nil
}

func (e *demoEngine) StartedAt(context.Context, string) (time.Time, error) { return e.start, nil }

// wave is a smooth deterministic signal in [0,1] that differs per container.
func wave(id string, t time.Duration) float64 {
	h := fnv.New32a()
	h.Write([]byte(id))
	ph := float64(h.Sum32()%628) / 100
	x := t.Seconds()
	return 0.5 + 0.3*math.Sin(x/5+ph) + 0.2*math.Sin(x/2.3+2*ph)
}

func (e *demoEngine) StreamStats(ctx context.Context, id string, onFrame func(Stats)) error {
	e.mu.Lock()
	s, i := e.spec(id)
	e.mu.Unlock()
	if i < 0 {
		return nil
	}
	const ncpu, step = 8, uint64(1e9)
	var total, system uint64 = 1e12, 1e13
	frame := func() {
		// A shared term keeps the summed CPU curve moving instead of averaging out.
		w := 0.5*wave(id, time.Since(e.boot)) + 0.5*wave("all", time.Since(e.boot))
		pct := s.cpu * (0.2 + 1.6*w)
		delta := uint64(pct / 100 / ncpu * float64(step))
		f := Stats{CPUTotal: total + delta, PreCPUTotal: total, CPUSystem: system + step, PreCPUSystem: system, OnlineCPUs: ncpu,
			MemUsage: uint64(s.mem * (0.95 + 0.1*w) * (1 << 20)), MemLimit: 16 << 30}
		total, system = f.CPUTotal, f.CPUSystem
		onFrame(f)
	}
	frame()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			frame()
		}
	}
}

func (e *demoEngine) Action(_ context.Context, id, verb string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, i := e.spec(id)
	if i < 0 {
		return fmt.Errorf("no such container: %s", id)
	}
	switch verb {
	case "start", "restart":
		e.ctrs[i].State, e.ctrs[i].Status = "running", "Up Less than a second"
	case "stop":
		e.ctrs[i].State, e.ctrs[i].Status = "exited", "Exited (0) Less than a second ago"
	default:
		return fmt.Errorf("unknown action %q", verb)
	}
	return nil
}

func (e *demoEngine) Logs(ctx context.Context, id string, tail int, follow bool, onLine func(stream byte, line string)) error {
	e.mu.Lock()
	s, i := e.spec(id)
	e.mu.Unlock()
	if i < 0 {
		return nil
	}
	base := time.Now().Add(-90 * time.Second)
	lines := []string{
		"listening on port " + strings.TrimPrefix(fmt.Sprint(firstPort(s)), "0"),
		"GET /healthz 200 1ms",
		"GET /api/products?limit=20 200 12ms",
		"GET /api/cart 200 4ms",
		"POST /api/orders 201 38ms",
		"GET /api/products/42 200 7ms",
	}
	// DOCKERDESK_DEMO_LOGLINES=N serves N lines of ~200 characters, for measuring the log view.
	if n, _ := strconv.Atoi(os.Getenv("DOCKERDESK_DEMO_LOGLINES")); n > 0 {
		lines = lines[:0]
		pad := strings.Repeat("x", 120)
		for k := 0; k < n; k++ {
			lines = append(lines, fmt.Sprintf("GET /api/products/%d?session=%08x 200 %dms ua=Mozilla/5.0 trace=%s", k, k*2654435761, k%90, pad))
		}
	}
	for k, l := range lines {
		onLine(1, base.Add(time.Duration(k*15)*time.Second).UTC().Format("2006-01-02T15:04:05Z")+" "+l)
	}
	if follow {
		<-ctx.Done()
	}
	return nil
}

func firstPort(s demoSpec) uint16 {
	if len(s.ports) == 0 {
		return 0
	}
	return s.ports[0].Private
}

func (e *demoEngine) Images(context.Context) ([]ImageInfo, error) {
	now := time.Now().Unix()
	return []ImageInfo{
		{ID: "sha256:" + strings.Repeat("a1", 32), RepoTags: []string{"ghcr.io/acme/shop-api:2.4.1"}, Size: 312 << 20, Created: now - 2*86400},
		{ID: "sha256:" + strings.Repeat("b2", 32), RepoTags: []string{"postgres:16"}, Size: 438 << 20, Created: now - 12*86400},
		{ID: "sha256:" + strings.Repeat("c3", 32), RepoTags: []string{"nginx:1.27-alpine"}, Size: 48 << 20, Created: now - 20*86400},
	}, nil
}

func (e *demoEngine) Volumes(context.Context) ([]VolumeInfo, error) {
	return []VolumeInfo{
		{Name: "shop_pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data"},
		{Name: "monitoring_grafana-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/monitoring_grafana-data/_data"},
	}, nil
}
