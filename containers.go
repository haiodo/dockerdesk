package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/haiodo/gowt"
)

// rowKey is stored in the tree item data: a group has only group set, a container has id too.
type rowKey struct{ group, id string }

const (
	colName = iota
	colID
	colImage
	colPorts
	colCPU
	colMemory
	colStarted
	colAction
	colMore
	colCount
)

type treeRow struct {
	key            rowKey
	depth          int
	cells          [colCount]string
	status, action string // icon names
}

var columns = [colCount]struct {
	title string
	width int
	right bool
}{
	{"Name", 230, false}, {"Container ID", 110, false}, {"Image", 200, false}, {"Port(s)", 120, false},
	{"CPU (%)", 80, true}, {"Memory", 170, true}, {"Last started", 120, false}, {"Actions", 60, false}, {"", 40, false},
}

func (u *ui) treeView() gowt.Option {
	return gowt.Tree(gowt.Grow(), gowt.Ref(&u.tree),
		gowt.OnSelect(func(*gowt.Node) { u.selectionChanged() }),
		gowt.OnActivate(func(n *gowt.Node) {
			if keyOf(n).id != "" {
				u.showLogs()
			}
		}),
		gowt.Init(func(t *gowt.TreeView) {
			t.ShowHeader(true)
			for _, c := range columns {
				var opts []gowt.Option
				if c.right {
					opts = append(opts, gowt.Right())
				}
				t.Column(c.title, c.width, opts...)
			}
			t.OnCellClick(u.onTreeClick)
			u.menu = gowt.PopupMenu(t)
			u.menu.Item("Start", func() { u.act("start") })
			u.menu.Item("Stop", func() { u.act("stop") })
			u.menu.Item("Restart", func() { u.act("restart") })
			u.menu.Separator()
			u.menu.Item("View logs", u.showLogs)
		}))
}

func keyOf(n *gowt.Node) rowKey {
	k, _ := n.Data().(rowKey)
	return k
}

func (u *ui) selectedKey() (rowKey, bool) {
	sel := u.tree.Selected()
	if len(sel) == 0 {
		return rowKey{}, false
	}
	return keyOf(sel[0]), true
}

// targetsOf resolves a row key to the visible containers it stands for: a group means its members
// that pass the current search and running filters, exactly the ones the row shows.
func targetsOf(visible []*Ctr, k rowKey) []*Ctr {
	var out []*Ctr
	for _, c := range visible {
		if (k.id != "" && c.ID == k.id) || (k.id == "" && c.Labels[composeLabel] == k.group) {
			out = append(out, c)
		}
	}
	return out
}

// onTreeClick handles the two icon columns. Only a plain left click counts, and each icon has a
// column of its own, so no offsets inside a cell are involved.
func (u *ui) onTreeClick(c gowt.TreeClick) {
	if c.Button != 1 || c.Count != 1 || c.Node == nil || (c.Col != colAction && c.Col != colMore) {
		return
	}
	k, ok := c.Node.Data().(rowKey)
	if !ok {
		return
	}
	u.tree.Select(c.Node)
	u.selectionChanged()
	if c.Col == colMore {
		u.menu.Show()
		return
	}
	targets := targetsOf(u.visible.Peek(), k)
	verb := "start"
	if anyRunning(targets) {
		verb = "stop"
	}
	u.actOn(verb, targets, k.id == "")
}

func anyRunning(cs []*Ctr) bool {
	return slices.ContainsFunc(cs, func(c *Ctr) bool { return c.Running() })
}

func (u *ui) selectionChanged() {
	u.selID, u.selName = "", ""
	if k, ok := u.selectedKey(); ok && k.id != "" {
		for _, c := range u.ctrs.Peek() {
			if c.ID == k.id {
				u.selID, u.selName = c.ID, c.Name()
			}
		}
	}
	u.selHist = nil
	u.redrawChart()
}

func buildRows(groups []*Group, now time.Time) []treeRow {
	var rows []treeRow
	for _, g := range groups {
		depth := 0
		if g.Name != "" {
			rows = append(rows, groupRow(g))
			depth = 1
		}
		for _, c := range g.Ctrs {
			rows = append(rows, ctrRow(c, depth, now))
		}
	}
	return rows
}

func groupRow(g *Group) treeRow {
	cpu, mem := sumUsage(g.Ctrs)
	run := 0
	for _, c := range g.Ctrs {
		if c.Running() {
			run++
		}
	}
	r := treeRow{key: rowKey{group: g.Name}, status: "stopped", action: "play"}
	switch {
	case run == len(g.Ctrs):
		r.status = "running"
	case run > 0:
		r.status = "partial"
	}
	if run > 0 {
		r.action = "stop"
	}
	r.cells = [colCount]string{g.Name, "-", "-", "-", cpuText(cpu, run > 0, false), memText(mem, 0, run > 0, false), "-"}
	return r
}

func ctrRow(c *Ctr, depth int, now time.Time) treeRow {
	r := treeRow{key: rowKey{group: c.Labels[composeLabel], id: c.ID}, depth: depth, status: "stopped", action: "play"}
	if c.Running() {
		r.status, r.action = "running", "stop"
	}
	r.cells = [colCount]string{c.Name(), c.ShortID(), c.Image, dash(formatPorts(c.Ports)),
		cpuText(c.CPU, c.Running(), c.NoStats), memText(c.MemUsed, c.MemLimit, c.Running(), c.NoStats), formatAgo(c.Started, now)}
	return r
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cpuText(v float64, running, failed bool) string {
	switch {
	case failed:
		return "N/A"
	case !running || v < 0.005:
		return "0%"
	}
	return fmt.Sprintf("%.2f%%", v)
}

// memText shows "used / limit"; a group has no single limit, so it shows only the sum.
func memText(used, limit int64, running, failed bool) string {
	switch {
	case failed:
		return "N/A"
	case !running:
		return "0B / 0B"
	case limit == 0:
		return formatBytes(used)
	}
	return formatBytes(used) + " / " + formatBytes(limit)
}

// render updates only the cells that changed when the set of rows is unchanged, so selection,
// scroll and expansion survive the 2 s refresh; a changed set rebuilds the tree.
func (u *ui) render(rows []treeRow) {
	if slices.EqualFunc(rows, u.rows, func(a, b treeRow) bool { return a.key == b.key && a.depth == b.depth }) {
		u.updateRows(rows)
	} else {
		u.rebuild(rows)
	}
}

func (u *ui) updateRows(rows []treeRow) {
	for i := range rows {
		r, old, n := &rows[i], &u.rows[i], u.items[i]
		for c := range r.cells {
			if r.cells[c] != old.cells[c] {
				n.SetTextAt(c, r.cells[c])
			}
		}
		if r.status != old.status {
			n.SetImageAt(colName, u.rowIcons[r.status])
		}
		if r.action != old.action {
			n.SetImageAt(colAction, u.rowIcons[r.action])
		}
	}
	u.rows = rows
}

func (u *ui) rebuild(rows []treeRow) {
	selected, hadSel := u.selectedKey()
	expanded := map[string]bool{}
	for _, n := range u.items {
		if k := keyOf(n); k.id == "" && n.Expanded() {
			expanded[k.group] = true
		}
	}
	u.tree.Batch(func() {
		u.tree.Clear()
		u.items = u.items[:0]
		var parent, reselect *gowt.Node
		for _, r := range rows {
			var n *gowt.Node
			if r.depth == 0 {
				n = u.tree.Node("")
				parent = n
			} else {
				n = parent.Node("")
			}
			n.SetData(r.key)
			u.fillNode(n, r)
			u.items = append(u.items, n)
			if hadSel && r.key == selected {
				reselect = n
			}
		}
		// New groups start expanded; a group the user collapsed stays collapsed.
		for i, r := range rows {
			if r.key.id == "" && (expanded[r.key.group] || !u.seen[r.key.group]) {
				u.items[i].SetExpanded(true)
				u.seen[r.key.group] = true
			}
		}
		if reselect != nil {
			u.tree.Select(reselect)
		}
	})
	u.rows = rows
}

func (u *ui) fillNode(n *gowt.Node, r treeRow) {
	for c, s := range r.cells {
		n.SetTextAt(c, s)
	}
	n.SetImageAt(colName, u.rowIcons[r.status])
	n.SetImageAt(colAction, u.rowIcons[r.action])
	n.SetImageAt(colMore, u.rowIcons["more"])
}
