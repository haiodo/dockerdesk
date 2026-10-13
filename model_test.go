package main

import (
	"strings"
	"testing"
	"time"
)

func mkCtr(id, name, proj, state string) *Ctr {
	l := map[string]string{}
	if proj != "" {
		l[composeLabel] = proj
	}
	return &Ctr{ContainerInfo: ContainerInfo{ID: id + strings.Repeat("0", 12), Names: []string{"/" + name}, Labels: l, State: state, Image: "img"}}
}

func TestGroupAndFilter(t *testing.T) {
	all := []*Ctr{mkCtr("a", "solo", "", "running"), mkCtr("b", "web", "zeta", "exited"), mkCtr("c", "db", "alpha", "running")}
	gs := groupContainers(all)
	if len(gs) != 3 || gs[0].Name != "alpha" || gs[1].Name != "zeta" || gs[2].Name != "" {
		t.Fatalf("groups %+v", gs)
	}
	if got := filterContainers(all, "", true); len(got) != 2 {
		t.Fatalf("running %d", len(got))
	}
	if got := filterContainers(all, "WEB", false); len(got) != 1 || got[0].Name() != "web" {
		t.Fatalf("search %v", got)
	}
}

func TestCPUPercentEmpty(t *testing.T) {
	if cpuPercent(Stats{}) != 0 {
		t.Fatal("empty sample must give 0")
	}
}

func TestTargetsAreVisibleMembers(t *testing.T) {
	all := []*Ctr{
		mkCtr("a", "web", "p", "running"), mkCtr("b", "db", "p", "running"),
		mkCtr("c", "old", "p", "exited"), mkCtr("d", "solo", "", "running"),
	}
	vis := filterContainers(all, "web", false)
	if got := targetsOf(vis, rowKey{group: "p"}); len(got) != 1 || got[0].Name() != "web" {
		t.Fatalf("search filter: %v", got)
	}
	vis = filterContainers(all, "", true)
	if got := targetsOf(vis, rowKey{group: "p"}); len(got) != 2 {
		t.Fatalf("only running: %d", len(got))
	}
	if got := targetsOf(vis, rowKey{group: "p", id: all[2].ID}); len(got) != 0 {
		t.Fatal("a hidden container must not be a target")
	}
}

func TestRelevantVerbs(t *testing.T) {
	cs := []*Ctr{mkCtr("a", "up", "p", "running"), mkCtr("b", "off", "p", "exited")}
	for verb, want := range map[string]string{"start": "off", "stop": "up", "restart": "up"} {
		got := relevant(verb, cs)
		if len(got) != 1 || got[0].Name() != want {
			t.Errorf("%s: %v", verb, got)
		}
	}
}

func TestLineWriterCapsLongLine(t *testing.T) {
	var lines []string
	w := &lineWriter{stream: 1, onLine: func(_ byte, l string) { lines = append(lines, l) }}
	w.Write(make([]byte, maxLineBytes+1))
	if len(lines) != 1 || len(w.buf) != 0 {
		t.Fatalf("lines %d buf %d", len(lines), len(w.buf))
	}
}

func TestRowsAndCellText(t *testing.T) {
	run := mkCtr("a", "web", "p", "running")
	run.CPU, run.MemUsed, run.MemLimit = 12.345, 1<<30, 4<<30
	off := mkCtr("b", "old", "p", "exited")
	rows := buildRows(groupContainers([]*Ctr{run, off}), time.Now())
	if len(rows) != 3 || rows[0].status != "partial" || rows[0].action != "stop" || rows[1].depth != 1 {
		t.Fatalf("%+v", rows)
	}
	if got := rows[2].cells; got[colCPU] != "12.35%" && got[colCPU] != "0%" {
		t.Fatalf("cpu %q", got[colCPU])
	}
	if cpuText(0, false, false) != "0%" || cpuText(1, true, true) != "N/A" || memText(0, 0, false, false) != "0B / 0B" {
		t.Fatal("cell text")
	}
}
