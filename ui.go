package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/look"
	"github.com/haiodo/gowt/rx"
)

const (
	pageContainers = iota
	pageImages
	pageVolumes
	pageLogs
)

// ui holds the widgets and the state shown in them; everything here belongs to the UI thread.
type ui struct {
	app   *gowt.App
	win   *gowt.Window
	eng   Engine
	info  Info
	ctx   context.Context
	stop  context.CancelFunc
	poll  *poller
	trace bool // -mem: log the duration of every apply

	pal      *rx.State[palette]
	fonts    fontSet
	rowIcons map[string]*gowt.Image
	navIcons *rx.State[[]*gowt.Image] // tinted for the current palette

	native  bool // macOS: native toolbar and glass sidebar
	hist    *navHistory
	page    *rx.Derived[int] // the page of the current place
	nav     *gowt.CanvasView // painted sidebar of Linux and Windows
	chartOn *rx.State[bool]

	// The poller posts whole snapshots; the rest is derived from them and from the user's input.
	snap    *rx.State[*snapshot] // nil until the first poll answers
	ctrs    *rx.Derived[[]*Ctr]  // the last successful poll
	use     *rx.Derived[usage]
	engine  *rx.Derived[int]
	pollErr *rx.Derived[string]
	status  *rx.State[string] // footer message until the next poll
	failure *rx.State[string] // last failed action, kept in the footer until the next action
	query   *rx.State[string]
	onlyRun *rx.State[bool]
	logID   *rx.State[string] // every Set restarts the stream, the same container included
	tail    *rx.State[int]    // index into tailChoices
	follow  *rx.State[bool]
	visible *rx.Derived[[]*Ctr]
	shown   *rx.Derived[[]treeRow] // the tree rows for visible

	tree               *gowt.TreeView
	menu               *gowt.Menu
	chart              *gowt.CanvasView
	rows               []treeRow
	items              []*gowt.Node
	seen               map[string]bool
	selID, selName     string
	totalHist, selHist history

	logText *gowt.StyledTextView
	logCur  *logStream
}

type usage struct {
	cpu float64
	mem int64
}

func (u *ui) initState() {
	u.snap = rx.NewFunc[*snapshot](nil, nil)
	u.ctrs = rx.ComputeFunc(func() []*Ctr {
		if s := u.snap.Get(); s != nil {
			return s.ctrs
		}
		return nil
	}, sameList)
	u.use = rx.Compute(func() usage { c, m := sumUsage(u.ctrs.Get()); return usage{c, m} })
	u.engine = rx.Compute(func() int {
		switch s := u.snap.Get(); {
		case s == nil:
			return engineConnecting
		case s.err != nil:
			return engineDown
		}
		return engineUp
	})
	u.pollErr = rx.Compute(func() string {
		if s := u.snap.Get(); s != nil && s.err != nil {
			return s.err.Error()
		}
		return ""
	})
	u.status, u.failure, u.query = rx.New(""), rx.New(""), rx.New("")
	u.onlyRun, u.follow = rx.New(false), rx.New(true)
	u.logID = rx.NewFunc("", nil)
	u.tail = rx.New(1)
	u.visible = rx.ComputeFunc(func() []*Ctr { return filterContainers(u.ctrs.Get(), u.query.Get(), u.onlyRun.Get()) }, nil)
	u.shown = rx.ComputeFunc(func() []treeRow { return buildRows(groupContainers(u.visible.Get()), time.Now()) }, nil)
	u.hist = newNavHistory(place{page: pageContainers})
	u.page = rx.Compute(func() int { return u.hist.cur.Get().page })
	u.pal = rx.New(paletteFor(u.app.Dark()))
	u.chartOn = rx.New(os.Getenv("DOCKERDESK_CHARTS") == "1")
}

// sameList compares by identity: post keeps the previous list after a failed poll. Empty lists
// from two polls compare equal; the chart follows snap, not this, so it still ticks.
func sameList(a, b []*Ctr) bool {
	return len(a) == len(b) && (a == nil) == (b == nil) && (len(a) == 0 || &a[0] == &b[0])
}

// post hands a poll result to the UI thread. A failed poll keeps the last good list, so the
// tree, the stats and the chart stay as they were while the engine is unreachable.
func (u *ui) post(s snapshot, good *[]*Ctr) {
	if ab.noApply {
		return
	}
	if s.err == nil {
		*good = s.ctrs
	}
	s.ctrs = *good
	if !u.trace {
		u.snap.Post(&s)
		return
	}
	u.app.Async(func() {
		t0 := time.Now()
		u.snap.Set(&s)
		u.app.Flush() // BindText lays out on the next loop turn; count it here
		log.Printf("apply %s", time.Since(t0).Round(time.Microsecond))
	})
}

func (u *ui) build() {
	u.initState()
	u.seen = map[string]bool{}
	w := u.app.Window("dockerdesk")
	u.win = w
	u.native = look.SetToolbar(w,
		look.ToolbarItem{Title: "Back/Forward", Nav: true, Group: []look.ToolbarItem{
			{Title: "Back", Symbol: "chevron.backward", OnClick: u.hist.Back, Enabled: u.hist.canBack},
			{Title: "Forward", Symbol: "chevron.forward", OnClick: u.hist.Forward, Enabled: u.hist.canFwd},
		}},
		look.ToolbarItem{Space: true},
		look.ToolbarItem{Title: "Search", OnSearch: u.search},
	)
	u.initFonts(&w.Unwrap().Control)
	u.loadRowIcons()
	u.navIcons = rx.NewFunc(u.loadNavIcons(u.pal.Peek()), nil)
	var sidebar, content *gowt.Panel
	w.Add(
		u.sidebar(gowt.Ref(&sidebar)),
		gowt.VBox(gowt.Ref(&content), gowt.Gap(0), u.paint(surfContent, inkNone),
			gowt.Pages(gowt.Grow(), u.paint(surfContent, inkNone), gowt.Init(func(p *gowt.PagesView) { gowt.Watch(p, u.page, p.Show) }),
				u.containersPage(),
				u.listPage(pageImages, "Images", []string{"Tags", "ID", "Size", "Created"}, []int{420, 130, 100, 130}, u.imageRows),
				u.listPage(pageVolumes, "Volumes", []string{"Name", "Driver", "Mount point"}, []int{380, 100, 480}, u.volumeRows),
				u.logsPage()),
			u.footer()))
	look.SetSidebar(w, sidebar, content, sidebarWidth)
	u.app.OnThemeChange(u.onThemeChange)
	back, fwd := navKeys(runtime.GOOS)
	w.OnKey(back, u.hist.Back)
	w.OnKey(fwd, u.hist.Forward)
	u.react()
	w.OnClose(func() bool { u.stop(); return true })
	// A fixed 1360x820 overflows smaller screens (the 1280x1024 Linux stand), cutting the right edge.
	wd, ht := 1360, 820
	if sw, sh := u.app.ScreenSize(); sw > 0 && sh > 0 {
		wd, ht = min(wd, sw), min(ht, sh)
	}
	w.SetSize(wd, ht)
	w.Show()
}

// textOf binds a label's text to f; DOCKERDESK_NOLABELS=1 leaves the text as created.
func textOf(f func() string) gowt.Option {
	if ab.noLabels {
		return gowt.Init(func(gowt.Widget) {})
	}
	return gowt.BindText(rx.Compute(f))
}

// pagePanel is one page of the content area.
func (u *ui) pagePanel(children ...gowt.Option) gowt.Option {
	return gowt.VBox(append([]gowt.Option{gowt.Padding(gowt.Spacing.PageMargin), gowt.Gap(gowt.Spacing.MD), u.paint(surfContent, inkNone)}, children...)...)
}

func (u *ui) title(text string) gowt.Option {
	return gowt.Label(text, u.paint(surfContent, inkText), u.fonts.title)
}

// search filters the containers from the toolbar search field and shows them.
func (u *ui) search(text string) {
	u.query.Set(text)
	if text != "" {
		u.hist.Open(place{page: pageContainers})
	}
}

func (u *ui) footer() gowt.Option {
	return gowt.Grid(2, gowt.GrowX(), gowt.Padding(gowt.Spacing.SM), gowt.Gap(gowt.Spacing.MD), u.paint(surfFooter, inkNone),
		gowt.Label("Connecting...", gowt.GrowX(), u.paint(surfFooter, inkText), u.fonts.small, textOf(func() string {
			if s := u.status.Get(); s != "" {
				return s
			}
			if e := u.pollErr.Get(); e != "" {
				return "Engine not reachable: " + e
			}
			if u.engine.Get() == engineConnecting {
				return "Connecting..."
			}
			x := u.use.Get()
			msg := fmt.Sprintf("Containers: RAM %s   CPU %.2f%%", formatBytes(x.mem), x.cpu/float64(max(u.info.NCPU, 1)))
			if f := u.failure.Get(); f != "" {
				msg += "   |   last action failed: " + f
			}
			return msg
		})),
		gowt.Label("Docker "+u.info.ServerVersion, gowt.Place(gowt.AlignEnd, gowt.AlignStart), u.paint(surfFooter, inkBlue), u.fonts.small))
}

func (u *ui) containersPage() gowt.Option {
	count := gowt.Init(func(gowt.Widget) {})
	if !ab.noTree {
		count = textOf(func() string { return fmt.Sprintf("Showing %d items", len(u.shown.Get())) })
	}
	return u.pagePanel(
		u.title("Containers"),
		u.stats(),
		gowt.Button("Only show running containers", gowt.Check(), gowt.Bind(u.onlyRun)),
		u.treeView(),
		gowt.Canvas(u.paintChart, gowt.GrowX(), gowt.Size(0, 170), gowt.Ref(&u.chart), gowt.BindVisible(u.chartOn),
			gowt.Init(func(c *gowt.CanvasView) { gowt.Watch(c, u.pal, func(palette) { c.Redraw() }) })),
		gowt.Label("Showing 0 items", gowt.Place(gowt.AlignEnd, gowt.AlignStart), u.paint(surfContent, inkText), u.fonts.small, count))
}

// stats puts the usage on a grouped card; its panels and labels paint no background of their
// own, so they take the card colour.
func (u *ui) stats() gowt.Option {
	return gowt.Section(gowt.GrowX(),
		gowt.Grid(3, gowt.Padding(gowt.Spacing.SM), gowt.Gap(gowt.Spacing.XL),
			gowt.VBox(gowt.GrowX(), gowt.Gap(gowt.Spacing.XS),
				gowt.Label("Container CPU usage", u.paint(surfNone, inkText), u.fonts.small),
				gowt.Grid(2, gowt.Gap(gowt.Spacing.SM),
					gowt.Label("-", u.paint(surfNone, inkGreen), u.fonts.big, textOf(func() string {
						if u.engine.Get() == engineConnecting {
							return "-"
						}
						return fmt.Sprintf("%.2f%% / %d00%%", u.use.Get().cpu, u.info.NCPU)
					})),
					gowt.Label(fmt.Sprintf("(%d CPUs available)", u.info.NCPU), u.paint(surfNone, inkMuted), u.fonts.small))),
			gowt.VBox(gowt.GrowX(), gowt.Gap(gowt.Spacing.XS),
				gowt.Label("Container memory usage", u.paint(surfNone, inkText), u.fonts.small),
				gowt.Label("-", u.paint(surfNone, inkGreen), u.fonts.big, textOf(func() string {
					if u.engine.Get() == engineConnecting {
						return "-"
					}
					return formatBytes(u.use.Get().mem) + " / " + formatBytes(u.info.MemTotal)
				}))),
			gowt.Link("", gowt.Place(gowt.AlignEnd, gowt.AlignStart),
				gowt.OnClick(func(string) { u.chartOn.Set(!u.chartOn.Peek()) }),
				gowt.BindText(rx.Compute(func() string {
					if u.chartOn.Get() {
						return `<a href="toggle">Hide charts</a>`
					}
					return `<a href="toggle">Show charts</a>`
				})))))
}

const (
	engineConnecting = iota
	engineUp
	engineDown
)

// react starts what follows the polls, the history and the user's input beyond the bound widgets.
func (u *ui) react() {
	gowt.Watch(u.win, u.page, func(p int) { u.win.SetTitle(navTitles[p]) })
	gowt.Watch(u.win, u.hist.cur, func(p place) {
		if p.page == pageLogs && p.logID != "" && p.logID != u.logID.Peek() {
			u.logID.Set(p.logID)
		}
	})
	rx.Effect(u.streamLogs)
	rx.Effect(func() {
		if e := u.pollErr.Get(); e != "" {
			log.Printf("engine error: %v", e)
		}
	})
	// A new poll ends the footer message an action left there.
	rx.Effect(func() { u.snap.Get(); u.status.Set("") })
	gowt.Watch(u.chart, u.snap, func(s *snapshot) {
		if s == nil || s.err != nil {
			return
		}
		u.totalHist.push(u.use.Peek().cpu)
		u.pushSelected()
		u.redrawChart()
	})
	if !ab.noTree {
		gowt.Watch(u.tree, u.shown, u.render)
	}
}

func (u *ui) pushSelected() {
	for _, c := range u.ctrs.Peek() {
		if c.ID == u.selID {
			u.selHist.push(c.CPU)
			return
		}
	}
	u.selHist = nil
}
