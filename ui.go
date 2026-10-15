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
	"github.com/haiodo/gowt/swt"
)

const (
	pageContainers = iota
	pageImages
	pageVolumes
	pageLogs
)

var (
	fillBoth = gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, VAlign: gowt.AlignFill, GrowX: true, GrowY: true})
	fillX    = gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, GrowX: true})
	alignEnd = gowt.Cell(gowt.GridCell{Align: gowt.AlignEnd})
)

// container is what Window and Panel share for building children.
type container interface {
	Panel(opts ...gowt.Option) *gowt.Panel
	Label(text string, opts ...gowt.Option) *gowt.Label
}

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

	pal      palette
	colors   map[gowt.RGB]*swt.Color
	restyle  []func()
	fonts    fontSet
	rowIcons map[string]*gowt.Image
	navIcons []*gowt.Image

	nav       *gowt.Canvas
	page      int
	pages     *gowt.Panel
	pageViews []*gowt.Panel

	count      *gowt.Label
	chartLink  *gowt.Link
	chartShown bool

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

	tree               *gowt.Tree
	menu               *gowt.Menu
	chart              *gowt.Canvas
	visible            []*Ctr
	rows               []treeRow
	items              []*swt.TreeItem
	seen               map[string]bool
	selID, selName     string
	totalHist, selHist history

	logText  *gowt.StyledText
	logCur   *logStream
	logCount int

	images, volumes *listPage
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
		rx.Flush()
		u.app.Unwrap().RunDeferredLayouts() // BindText lays out on the next loop turn; count it here
		log.Printf("apply %s", time.Since(t0).Round(time.Microsecond))
	})
}

func (u *ui) build() {
	u.initState()
	u.seen = map[string]bool{}
	u.colors = map[gowt.RGB]*swt.Color{}
	u.pal = paletteFor(u.app.Dark())
	w := u.app.Window("dockerdesk")
	u.win = w
	// Content runs under the transparent title bar on macOS; the blue shell background fills that strip.
	look.SetFullSizeContent(w, true)
	if runtime.GOOS == "darwin" {
		w.SetTitle("")
	}
	u.initFonts(&w.Unwrap().Control)
	u.loadRowIcons()
	u.loadNavIcons()
	// One root panel under a Fill layout: the window layout would otherwise add the title bar height as a margin above the header.
	w.SetLayout(gowt.Fill{})
	root := w.Panel()
	root.SetLayout(gowt.Grid{Columns: 2})
	u.buildHeader(root)
	u.buildSidebar(root)
	u.pages = root.Panel(fillBoth)
	u.pages.SetLayout(gowt.Stack{})
	u.style(&u.pages.Unwrap().Control, surfContent, inkNone)
	u.buildPages()
	u.buildFooter(root)
	u.app.OnThemeChange(u.onThemeChange)
	u.react()
	w.OnClose(func() bool { u.stop(); return true })
	// A fixed 1360x820 overflows smaller screens (the 1280x1024 Linux stand), cutting the right edge.
	wd, ht := 1360, 820
	if a := w.Unwrap().GetDisplay().GetPrimaryMonitor().GetClientArea(); a.Width > 0 && a.Height > 0 {
		wd, ht = min(wd, int(a.Width)), min(ht, int(a.Height))
	}
	w.SetSize(wd, ht)
	w.Show()
}

// label makes a styled label; the font may be nil for the system default.
func (u *ui) label(p container, text string, s surface, i ink, f *swt.Font, opts ...gowt.Option) *gowt.Label {
	l := p.Label(text, opts...)
	u.style(&l.Unwrap().Control, s, i)
	if f != nil {
		l.Unwrap().SetFont(f)
	}
	return l
}

// panel makes a container painted with the surface colour.
func (u *ui) panel(p container, s surface, l gowt.Layout, opts ...gowt.Option) *gowt.Panel {
	c := p.Panel(opts...)
	c.SetLayout(l)
	u.style(&c.Unwrap().Control, s, inkNone)
	return c
}

const (
	headerHeight = 34 // about the macOS title bar: the traffic lights sit on its centre line
	lightsInset  = 80 // traffic lights end near 69 pt
	edgeInset    = 13 // the same gap the lights have from the window edge
)

// buildHeader is the compact blue row that stands in for the title bar on macOS. Every child grabs
// the vertical space and centres in it, so the name shares the traffic lights' centre line.
func (u *ui) buildHeader(root *gowt.Panel) {
	h := u.panel(root, surfHeader, gowt.Grid{Columns: 3}, gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, GrowX: true, SpanX: 2, Height: headerHeight}))
	// TODO(wt/facade02): enable dragging the window by this panel once the facade option is released.
	left := 0
	if runtime.GOOS == "darwin" {
		left = lightsInset
	}
	centered := gowt.GridCell{Align: gowt.AlignFill, VAlign: gowt.AlignCenter, GrowY: true}
	spacer := func(w int) {
		u.label(h, "", surfHeader, inkNone, nil, gowt.Cell(gowt.GridCell{Width: w, VAlign: gowt.AlignCenter, GrowY: true}))
	}
	spacer(left + 4)
	centered.GrowX = true
	u.label(h, "dockerdesk", surfHeader, inkWhite, u.fonts.brand, gowt.Cell(centered))
	spacer(edgeInset)
}

// searchField is a native search box (magnifier icon where the OS has one) that filters the containers.
func (u *ui) searchField(parent *swt.Composite, width int32) *swt.Text {
	t := swt.NewText(parent, swt.SEARCH|swt.ICON_SEARCH)
	t.SetMessage("Search")
	t.AddModifyListener(&modifier{func() { u.query.Set(t.GetText()) }})
	rd := swt.NewRowData()
	rd.Width = width
	t.SetLayoutData(rd)
	return t
}

type modifier struct{ f func() }

func (m *modifier) ModifyText(*swt.ModifyEvent) { m.f() }

func (u *ui) buildFooter(root *gowt.Panel) {
	f := u.panel(root, surfFooter, gowt.Grid{Columns: 2, Margin: gowt.Spacing.SM, Spacing: gowt.Spacing.MD}, gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, GrowX: true, SpanX: 2}))
	footL := u.label(f, "Connecting...", surfFooter, inkText, u.fonts.small, fillX)
	u.label(f, "Docker "+u.info.ServerVersion, surfFooter, inkBlue, u.fonts.small, alignEnd)
	if ab.noLabels {
		return
	}
	footL.BindText(rx.Compute(func() string {
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
	}))
}

func (u *ui) buildPages() {
	for range 4 {
		u.pageViews = append(u.pageViews, u.panel(u.pages, surfContent, gowt.Grid{Columns: 1, Margin: gowt.Spacing.PageMargin, Spacing: gowt.Spacing.MD}))
	}
	u.buildContainers(u.pageViews[pageContainers])
	u.images = u.newListPage(u.pageViews[pageImages], "Images", []string{"Tags", "ID", "Size", "Created"}, []int{420, 130, 100, 130}, u.imageRows)
	u.volumes = u.newListPage(u.pageViews[pageVolumes], "Volumes", []string{"Name", "Driver", "Mount point"}, []int{380, 100, 480}, u.volumeRows)
	u.buildLogs(u.pageViews[pageLogs])
	u.pages.ShowTop(u.pageViews[pageContainers])
}

func (u *ui) openPage(i int) {
	u.page = i
	u.pages.ShowTop(u.pageViews[i])
	u.nav.Redraw()
	switch i {
	case pageImages:
		u.reload(u.images)
	case pageVolumes:
		u.reload(u.volumes)
	}
}

func (u *ui) buildContainers(p *gowt.Panel) {
	u.label(p, "Containers", surfContent, inkText, u.fonts.title)
	u.buildStats(p)
	bar := u.panel(p, surfContent, gowt.Row{Spacing: gowt.Spacing.LG, Center: true}, fillX)
	u.searchField(bar.AsComposite(), 320)
	only := bar.Button("Only show running containers", nil, gowt.Check())
	only.BindChecked(u.onlyRun)
	u.style(&only.Unwrap().Control, surfNone, inkNone) // native colours: the system theme draws the check box
	u.buildTree(p)
	u.chart = p.Canvas(u.paintChart, gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, GrowX: true, Height: 170}))
	u.showChart(os.Getenv("DOCKERDESK_CHARTS") == "1")
	u.count = u.label(p, "Showing 0 items", surfContent, inkText, u.fonts.small, alignEnd)
}

func (u *ui) buildStats(p *gowt.Panel) {
	row := u.panel(p, surfContent, gowt.Grid{Columns: 3, Spacing: gowt.Spacing.XL}, fillX)
	card := gowt.Grid{Columns: 1, Spacing: gowt.Spacing.XS}
	cpu := u.panel(row, surfContent, card, fillX)
	u.label(cpu, "Container CPU usage", surfContent, inkText, u.fonts.small)
	v := u.panel(cpu, surfContent, gowt.Grid{Columns: 2, Spacing: gowt.Spacing.SM})
	cpuVal := u.label(v, "-", surfContent, inkGreen, u.fonts.big)
	u.label(v, fmt.Sprintf("(%d CPUs available)", u.info.NCPU), surfContent, inkMuted, u.fonts.small)
	mem := u.panel(row, surfContent, card, fillX)
	u.label(mem, "Container memory usage", surfContent, inkText, u.fonts.small)
	memVal := u.label(mem, "-", surfContent, inkGreen, u.fonts.big)
	if !ab.noLabels {
		cpuVal.BindText(rx.Compute(func() string {
			if u.engine.Get() == engineConnecting {
				return "-"
			}
			return fmt.Sprintf("%.2f%% / %d00%%", u.use.Get().cpu, u.info.NCPU)
		}))
		memVal.BindText(rx.Compute(func() string {
			if u.engine.Get() == engineConnecting {
				return "-"
			}
			return formatBytes(u.use.Get().mem) + " / " + formatBytes(u.info.MemTotal)
		}))
	}
	u.chartLink = row.Link(`<a href="toggle">Show charts</a>`, func(string) { u.showChart(!u.chartShown) }, alignEnd)
	u.style(&u.chartLink.Unwrap().Control, surfContent, inkNone)
}

// showChart hides the chart by excluding it from the grid, which the facade has no call for.
func (u *ui) showChart(on bool) {
	u.chartShown = on
	c := u.chart.Unwrap()
	c.GetLayoutData().(*swt.GridData).Exclude = !on
	c.SetVisible(on)
	text := "Show charts"
	if on {
		text = "Hide charts"
	}
	if u.chartLink != nil {
		u.chartLink.SetText(`<a href="toggle">` + text + `</a>`)
	}
	u.pageViews[pageContainers].Unwrap().Layout()
}

const (
	engineConnecting = iota
	engineUp
	engineDown
)

// react starts what follows the polls and the user's input beyond the bound labels.
func (u *ui) react() {
	if !ab.noLabels {
		gowt.Bind(u.nav, u.engine, func(int) { u.nav.Redraw() })
	}
	rx.Effect(func() {
		if e := u.pollErr.Get(); e != "" {
			log.Printf("engine error: %v", e)
		}
	})
	// A new poll ends the footer message an action left there.
	rx.Effect(func() { u.snap.Get(); u.status.Set("") })
	rx.Effect(func() {
		if s := u.snap.Get(); s == nil || s.err != nil {
			return
		}
		u.totalHist.push(u.use.Peek().cpu)
		u.pushSelected()
		u.redrawChart()
	})
	if ab.noTree {
		return
	}
	visible := rx.ComputeFunc(func() []*Ctr { return filterContainers(u.ctrs.Get(), u.query.Get(), u.onlyRun.Get()) }, nil)
	rows := rx.ComputeFunc(func() []treeRow { return buildRows(groupContainers(visible.Get()), time.Now()) }, nil)
	rx.Effect(func() {
		u.visible = visible.Get()
		u.render(rows.Get())
	})
	if !ab.noLabels {
		u.count.BindText(rx.Compute(func() string { return fmt.Sprintf("Showing %d items", len(rows.Get())) }))
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
