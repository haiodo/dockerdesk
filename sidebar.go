package main

import (
	"runtime"
	"slices"

	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/rx"
	"github.com/haiodo/gowt/swt"
)

var navTitles = []string{"Containers", "Images", "Volumes", "Logs"}

// navSections groups the pages under the sidebar headings, Finder style.
var navSections = []struct {
	title string
	pages []int
}{
	{"Docker", []int{pageContainers, pageImages, pageVolumes}},
	{"Inspect", []int{pageLogs}},
}

const (
	sidebarWidth = 220
	navIndent    = 16 // headings and the engine line start where the row icons do
)

// place is one entry of the page history; logID is the container shown on the Logs page.
type place struct {
	page  int
	logID string
}

// navHistory is the browser-style page history: Open pushes the current place on the back stack
// and clears forward, Back and Forward move between the two stacks.
type navHistory struct {
	cur             *rx.State[place]
	backs, fwds     []place
	canBack, canFwd *rx.State[bool]
}

func newNavHistory(start place) *navHistory {
	return &navHistory{cur: rx.New(start), canBack: rx.New(false), canFwd: rx.New(false)}
}

// Open goes to p; opening the current place does nothing.
func (h *navHistory) Open(p place) {
	c := h.cur.Peek()
	if p == c {
		return
	}
	h.backs = append(h.backs, c)
	h.fwds = h.fwds[:0]
	h.move(p)
}

func (h *navHistory) Back() { h.step(&h.backs, &h.fwds) }

func (h *navHistory) Forward() { h.step(&h.fwds, &h.backs) }

// step pops a place from one stack, pushes the current place on the other and goes to the popped one.
func (h *navHistory) step(from, to *[]place) {
	n := len(*from)
	if n == 0 {
		return
	}
	p := (*from)[n-1]
	*from = (*from)[:n-1]
	*to = append(*to, h.cur.Peek())
	h.move(p)
}

func (h *navHistory) move(p place) {
	h.cur.Set(p)
	h.canBack.Set(len(h.backs) > 0)
	h.canFwd.Set(len(h.fwds) > 0)
}

// placeOf is the place the sidebar opens for page: Logs keeps the container shown last.
func (u *ui) placeOf(page int) place {
	if page == pageLogs {
		return place{page: pageLogs, logID: u.logID.Peek()}
	}
	return place{page: page}
}

// buildSidebar makes the source list: a heading and a one-column table per section, the engine
// state at the bottom. surf is surfNone on macOS: look.SetSidebar clears backgrounds for the glass.
func (u *ui) buildSidebar(w *gowt.Window, surf surface) *gowt.Panel {
	sb := u.panel(w, surf, gowt.Grid{Columns: 1})
	for i, sec := range navSections {
		h := u.label(sb, sec.title, surf, inkMuted, u.fonts.heading, fillX)
		gd := h.Unwrap().GetLayoutData().(*swt.GridData)
		gd.HorizontalIndent, gd.VerticalIndent = navIndent, int32(gowt.Spacing.SM)
		if i > 0 {
			gd.VerticalIndent = int32(gowt.Spacing.LG)
		}
		t := sb.Table(gowt.NoBorder(), fillX)
		t.ShowHeader(false)
		t.ShowLines(false)
		u.style(&t.Unwrap().Control, surf, inkNone)
		col := t.Column("", sidebarWidth)
		t.Unwrap().AddControlListener(swt.ControlListenerControlResizedAdapter(func(*swt.ControlEvent) {
			col.SetWidth(int(t.Unwrap().GetClientArea().Width))
		}))
		for _, pg := range sec.pages {
			t.Row(navTitles[pg])
		}
		t.OnSelect(func(j int) {
			if j >= 0 {
				u.hist.Open(u.placeOf(sec.pages[j]))
			}
		})
		u.navTables = append(u.navTables, t)
	}
	u.setNavImages()
	u.buildEngineLine(sb, surf)
	return sb
}

func (u *ui) setNavImages() {
	for i, sec := range navSections {
		for j, pg := range sec.pages {
			u.navTables[i].RowAt(j).SetImage(0, u.navIcons[pg])
		}
	}
}

// buildEngineLine shows the engine state at the bottom of the sidebar: a coloured dot and a word.
func (u *ui) buildEngineLine(sb *gowt.Panel, surf surface) {
	line := u.panel(sb, surf, gowt.Grid{Columns: 2, Margin: gowt.Spacing.SM, Spacing: gowt.Spacing.SM},
		gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, VAlign: gowt.AlignEnd, GrowX: true, GrowY: true}))
	dot := u.label(line, "", surf, inkNone, nil, gowt.Cell(gowt.GridCell{VAlign: gowt.AlignCenter}))
	dot.Unwrap().GetLayoutData().(*swt.GridData).HorizontalIndent = int32(navIndent - gowt.Spacing.SM)
	dot.SetImage(u.rowIcons["wait"])
	text := u.label(line, "Connecting...", surf, inkMuted, u.fonts.small, fillX)
	if ab.noLabels {
		return
	}
	gowt.Bind(text, u.engine, func(e int) {
		icon, s := "wait", "Connecting..."
		switch e {
		case engineUp:
			icon, s = "running", "Engine running"
		case engineDown:
			icon, s = "down", "Engine not reachable"
		}
		dot.SetImage(u.rowIcons[icon])
		text.SetText(s)
	})
}

// show puts place p on screen; it runs whenever the history moves.
func (u *ui) show(p place) {
	if p.page == pageLogs && p.logID != "" && p.logID != u.logID.Peek() {
		u.logID.Set(p.logID)
	}
	u.pages.ShowTop(u.pageViews[p.page])
	for i, sec := range navSections {
		if j := slices.Index(sec.pages, p.page); j >= 0 {
			u.navTables[i].SetSelection(j)
		} else {
			u.navTables[i].SetSelection()
		}
	}
	if u.native {
		u.win.SetTitle(navTitles[p.page])
	}
	switch p.page {
	case pageImages:
		u.reload(u.images)
	case pageVolumes:
		u.reload(u.volumes)
	}
}

// navKeys moves through the history with Cmd+[ and Cmd+] on macOS, Alt+Left and Alt+Right elsewhere.
type navKeys struct{ h *navHistory }

func (k navKeys) HandleEvent(e *swt.Event) {
	switch navKey(runtime.GOOS, e.StateMask&swt.MODIFIER_MASK, e.KeyCode) {
	case -1:
		k.h.Back()
	case 1:
		k.h.Forward()
	default:
		return
	}
	e.Doit = false
}

// navKey maps a key press to -1 for back, 1 for forward and 0 for any other key.
func navKey(goos string, mods, key int32) int {
	back, fwd, want := int32(swt.ARROW_LEFT), int32(swt.ARROW_RIGHT), int32(swt.ALT)
	if goos == "darwin" {
		back, fwd, want = '[', ']', swt.COMMAND
	}
	switch {
	case mods != want:
		return 0
	case key == back:
		return -1
	case key == fwd:
		return 1
	}
	return 0
}
