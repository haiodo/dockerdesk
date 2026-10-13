package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const composeLabel = "com.docker.compose.project"

// Ctr is one container row: the list entry plus the latest sample.
type Ctr struct {
	ContainerInfo
	CPU      float64 // percent of one CPU, 100 per core
	MemUsed  int64
	MemLimit int64
	Started  time.Time
	NoStats  bool // the stats call failed; show N/A, not 0
}

// Group is a compose project, or the loose containers when Name is empty.
type Group struct {
	Name string
	Ctrs []*Ctr
}

func (c *Ctr) Running() bool { return c.State == "running" }

func (c *Ctr) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func (c *Ctr) ShortID() string { return c.ID[:12] }

// cpuPercent follows the docker CLI formula: container delta over host delta times online CPUs.
func cpuPercent(s Stats) float64 {
	// The first frame of a stream has no previous sample; its delta would be the whole uptime.
	if s.PreCPUSystem == 0 || s.CPUTotal <= s.PreCPUTotal || s.CPUSystem <= s.PreCPUSystem {
		return 0
	}
	cpuDelta := float64(s.CPUTotal - s.PreCPUTotal)
	sysDelta := float64(s.CPUSystem - s.PreCPUSystem)
	return cpuDelta / sysDelta * float64(max(s.OnlineCPUs, 1)) * 100
}

// memUsed drops the page cache the way `docker stats` does.
func memUsed(s Stats) int64 {
	if s.MemInactive > 0 && s.MemInactive < s.MemUsage {
		return int64(s.MemUsage - s.MemInactive)
	}
	return int64(s.MemUsage)
}

// groupContainers splits by compose project: projects sorted by name, loose containers last.
func groupContainers(cs []*Ctr) []*Group {
	byName := map[string]*Group{}
	var out []*Group
	for _, c := range cs {
		g := byName[c.Labels[composeLabel]]
		if g == nil {
			g = &Group{Name: c.Labels[composeLabel]}
			byName[g.Name] = g
			out = append(out, g)
		}
		g.Ctrs = append(g.Ctrs, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Name, out[j].Name
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	for _, g := range out {
		sort.SliceStable(g.Ctrs, func(i, j int) bool { return g.Ctrs[i].Name() < g.Ctrs[j].Name() })
	}
	return out
}

// filterContainers applies the search text (name, image, id) and the running-only toggle.
func filterContainers(cs []*Ctr, query string, onlyRunning bool) []*Ctr {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []*Ctr
	for _, c := range cs {
		if onlyRunning && !c.Running() {
			continue
		}
		hay := strings.ToLower(c.Name() + " " + c.Image + " " + c.ShortID())
		if q == "" || strings.Contains(hay, q) {
			out = append(out, c)
		}
	}
	return out
}

func formatPorts(ps []Port) string {
	seen := map[string]bool{}
	var parts []string
	for _, p := range ps {
		if p.Public == 0 {
			continue
		}
		s := fmt.Sprintf("%d:%d", p.Public, p.Private)
		if !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	v, suffix := float64(n), "KMGTPE"
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	if v >= 100 || suffix[i] == 'K' || suffix[i] == 'M' {
		return fmt.Sprintf("%.1f%ciB", v, suffix[i])
	}
	return fmt.Sprintf("%.2f%ciB", v, suffix[i])
}

func formatAgo(t time.Time, now time.Time) string {
	if t.IsZero() || t.Year() < 2 {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

// sumUsage totals CPU percent and memory over running containers.
func sumUsage(cs []*Ctr) (cpu float64, mem int64) {
	for _, c := range cs {
		if c.Running() {
			cpu += c.CPU
			mem += c.MemUsed
		}
	}
	return cpu, mem
}
