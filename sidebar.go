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
	navIndent    = 16 // macOS: headings and the engine line start where the row icons do
)

// Geometry of the painted sidebar of Linux and Windows.
const (
	navTop   = 16
	navRowH  = 38
	navInset = 12
	navIcon  = 18
	navHeadH = 24
	navGap   = 8
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

// sidebar makes the macOS source list (a heading and a one-column table per section, the engine
// state at the bottom) or the painted sidebar of Linux and Windows. On macOS look.SetSidebar clears
// the backgrounds for the glass.
func (u *ui) sidebar(ref gowt.Option) gowt.Option {
	if !u.native {
		return gowt.VBox(ref, gowt.Gap(0), u.paint(surfSidebar, inkNone),
			gowt.Canvas(u.paintNav, gowt.Grow(), gowt.Ref(&u.nav), gowt.Init(func(c *gowt.CanvasView) {
				c.OnMouseDown(u.onNavClick)
				gowt.Watch(c, u.page, func(int) { c.Redraw() })
				gowt.Watch(c, u.pal, func(palette) { c.Redraw() })
				if !ab.noLabels {
					gowt.Watch(c, u.engine, func(int) { c.Redraw() })
				}
			})))
	}
	sb := []gowt.Option{ref, gowt.Gap(0)}
	for i, sec := range navSections {
		top := gowt.Spacing.SM
		if i > 0 {
			top = gowt.Spacing.LG
		}
		sb = append(sb,
			gowt.Label(sec.title, gowt.GrowX(), u.paint(surfNone, inkMuted), u.fonts.heading, indent(navIndent, top)),
			u.navTable(sec.pages))
	}
	return gowt.VBox(append(sb, u.engineLine())...)
}

// navTable lists the pages of one sidebar section; selectNav marks the current one.
func (u *ui) navTable(pages []int) gowt.Option {
	return gowt.Table(gowt.NoBorder(), gowt.GrowX(),
		gowt.Init(func(t *gowt.TableView) {
			t.ShowHeader(false)
			t.ShowLines(false)
			// No column: with one as wide as the table the selection pill ran to the sidebar edge.
			for _, pg := range pages {
				t.Row(navTitles[pg])
			}
			t.OnSelect(func(j int) {
				if j >= 0 {
					u.hist.Open(u.placeOf(pages[j]))
				}
			})
			gowt.Watch(t, u.navIcons, func(icons []*gowt.Image) {
				for j, pg := range pages {
					t.RowAt(j).SetImage(0, icons[pg])
				}
			})
			u.navTables = append(u.navTables, t)
		}))
}

// selectNav marks page p in the macOS source list.
func (u *ui) selectNav(p int) {
	for i, t := range u.navTables {
		if j := slices.Index(navSections[i].pages, p); j >= 0 {
			t.SetSelection(j)
		} else {
			t.SetSelection()
		}
	}
}

// indent moves a label right and down inside its grid cell, which no placement option does.
func indent(x, y int) gowt.Option {
	return gowt.Init(func(l *gowt.LabelView) {
		gd := l.Unwrap().GetLayoutData().(*swt.GridData)
		gd.HorizontalIndent, gd.VerticalIndent = int32(x), int32(y)
	})
}

// navRow is a line of the painted sidebar: a section heading (page -1) or a page.
type navRow struct {
	page  int
	title string
	y     int
}

func navRows() []navRow {
	var rows []navRow
	y := navTop
	for i, sec := range navSections {
		if i > 0 {
			y += navGap
		}
		rows = append(rows, navRow{-1, sec.title, y})
		y += navHeadH
		for _, pg := range sec.pages {
			rows = append(rows, navRow{pg, navTitles[pg], y})
			y += navRowH
		}
	}
	return rows
}

func (u *ui) paintNav(g *gowt.GC) {
	p := u.pal.Peek()
	icons := u.navIcons.Peek()
	w, h := u.nav.Size()
	g.SetFill(p.sidebar)
	g.FillRect(0, 0, w, h)
	cur := u.page.Peek()
	for _, r := range navRows() {
		if r.page < 0 {
			font := g.Unwrap().GetFont()
			g.Unwrap().SetFont(u.fonts.navHeading)
			g.SetColor(p.muted)
			_, th := g.TextSize(r.title)
			g.Text(r.title, navInset+12, r.y+(navHeadH-th)/2)
			g.Unwrap().SetFont(font)
			continue
		}
		rh := navRowH - 4
		bg := p.sidebar
		if r.page == cur {
			bg = p.selected
		}
		g.SetFill(bg)
		g.FillRect(navInset, r.y, w-2*navInset, rh)
		g.Image(icons[r.page], navInset+12, r.y+(rh-navIcon)/2)
		g.SetColor(p.text)
		_, th := g.TextSize(r.title)
		g.Text(r.title, navInset+12+navIcon+12, r.y+(rh-th)/2)
	}
	u.paintEngine(g, h)
}

// paintEngine draws the engine state at the bottom of the sidebar.
func (u *ui) paintEngine(g *gowt.GC, h int) {
	p := u.pal.Peek()
	dot, text := p.muted, "Connecting..."
	switch u.engine.Peek() {
	case engineUp:
		dot, text = gowt.RGB{R: 46, G: 159, B: 107}, "Engine running"
	case engineDown:
		dot, text = gowt.RGB{R: 214, G: 69, B: 65}, "Engine not reachable"
	}
	y := h - 30
	g.SetFill(dot)
	g.FillOval(navInset+14, y+4, 10, 10)
	g.SetFill(p.sidebar)
	g.SetColor(p.muted)
	g.Text(text, navInset+14+10+10, y)
}

// onNavClick opens the page under a plain left click.
func (u *ui) onNavClick(m gowt.Mouse) {
	if m.Button != 1 || m.Count != 1 {
		return
	}
	w, _ := u.nav.Size()
	for _, r := range navRows() {
		if r.page >= 0 && m.X >= navInset && m.X < w-navInset && m.Y >= r.y && m.Y < r.y+navRowH-4 {
			u.hist.Open(u.placeOf(r.page))
			return
		}
	}
}

// engineLine shows the engine state at the bottom of the sidebar: a coloured dot and a word.
func (u *ui) engineLine() gowt.Option {
	return gowt.Grid(2, gowt.Padding(gowt.Spacing.SM), gowt.Gap(gowt.Spacing.SM), gowt.Grow(), gowt.Place(gowt.AlignFill, gowt.AlignEnd),
		gowt.Label("", gowt.Place(gowt.AlignStart, gowt.AlignCenter), indent(navIndent-gowt.Spacing.SM, 0), gowt.Init(func(l *gowt.LabelView) {
			l.SetImage(u.rowIcons["wait"])
			if !ab.noLabels {
				gowt.Watch(l, u.engine, func(e int) { l.SetImage(u.rowIcons[engineIcon(e)]) })
			}
		})),
		gowt.Label("Connecting...", gowt.GrowX(), u.paint(surfNone, inkMuted), u.fonts.small, textOf(func() string { return engineText(u.engine.Get()) })))
}

func engineIcon(e int) string {
	switch e {
	case engineUp:
		return "running"
	case engineDown:
		return "down"
	}
	return "wait"
}

func engineText(e int) string {
	switch e {
	case engineUp:
		return "Engine running"
	case engineDown:
		return "Engine not reachable"
	}
	return "Connecting..."
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
