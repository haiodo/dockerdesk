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

// lbl is a label that touches the widget only when the text changed.
type lbl struct {
	l    *gowt.Label
	last string
}

func (l *lbl) set(s string) bool {
	if s == l.last {
		return false
	}
	l.last = s
	l.l.SetText(s)
	return true
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
	engine    int // engineConnecting until the first poll answers
	lastErr   string
	pages     *gowt.Panel
	pageViews []*gowt.Panel

	cpuVal, cpuSub, memVal, count, footL, footR *lbl
	chartLink                                   *gowt.Link
	chartShown                                  bool

	query              string
	only               *gowt.Button
	tree               *gowt.Tree
	menu               *gowt.Menu
	chart              *gowt.Canvas
	all, visible       []*Ctr
	rows               []treeRow
	items              []*swt.TreeItem
	seen               map[string]bool
	selID, selName     string
	totalHist, selHist history

	logTitle  *gowt.Label
	logTail   *gowt.Combo
	logFollow *gowt.Button
	logText   *gowt.StyledText
	logID     string
	logCancel context.CancelFunc
	logCur    *logStream
	logCount  int

	images, volumes *listPage
	failure         string // last failed action, kept in the footer until the next action
}

func (u *ui) build() {
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
func (u *ui) label(p container, text string, s surface, i ink, f *swt.Font, opts ...gowt.Option) *lbl {
	l := p.Label(text, opts...)
	u.style(&l.Unwrap().Control, s, i)
	if f != nil {
		l.Unwrap().SetFont(f)
	}
	return &lbl{l, text}
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
	t.AddModifyListener(&modifier{func() {
		u.query = t.GetText()
		u.render()
	}})
	rd := swt.NewRowData()
	rd.Width = width
	t.SetLayoutData(rd)
	return t
}

type modifier struct{ f func() }

func (m *modifier) ModifyText(*swt.ModifyEvent) { m.f() }

func (u *ui) buildFooter(root *gowt.Panel) {
	f := u.panel(root, surfFooter, gowt.Grid{Columns: 2, Margin: gowt.Spacing.SM, Spacing: gowt.Spacing.MD}, gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, GrowX: true, SpanX: 2}))
	u.footL = u.label(f, "Connecting...", surfFooter, inkText, u.fonts.small, fillX)
	u.footR = u.label(f, "", surfFooter, inkBlue, u.fonts.small, alignEnd)
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
	u.only = bar.Button("Only show running containers", func() { u.render() }, gowt.Check())
	u.style(&u.only.Unwrap().Control, surfNone, inkNone) // native colours: the system theme draws the check box
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
	u.cpuVal = u.label(v, "-", surfContent, inkGreen, u.fonts.big)
	u.cpuSub = u.label(v, "", surfContent, inkMuted, u.fonts.small)
	mem := u.panel(row, surfContent, card, fillX)
	u.label(mem, "Container memory usage", surfContent, inkText, u.fonts.small)
	u.memVal = u.label(mem, "-", surfContent, inkGreen, u.fonts.big)
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

// setEngine records reachability; only a change repaints the sidebar.
func (u *ui) setEngine(state int) {
	if state != u.engine {
		u.engine = state
		if !ab.noLabels {
			u.nav.Redraw()
		}
	}
}

// apply takes a poll result on the UI thread. Per cycle it touches: setEngine (sidebar redraw only
// on a change), render (tree cell updates for changed cells only, or a rebuild when the row set
// changed), the stat and footer labels (only when their text changed) and the chart canvas (only
// while shown). It does not layout, pack columns, create images or toggle redraw outside rebuild.
func (u *ui) apply(s snapshot) {
	if ab.noApply {
		return
	}
	t0 := time.Now()
	defer func() {
		if u.trace {
			log.Printf("apply %s", time.Since(t0).Round(time.Microsecond))
		}
	}()
	if s.err != nil {
		u.applyError(s.err)
		return
	}
	u.all = s.ctrs
	cpu, mem := sumUsage(u.all)
	u.totalHist.push(cpu)
	u.pushSelected()
	if !ab.noTree {
		u.render()
	}
	if ab.noLabels {
		return
	}
	u.setEngine(engineUp)
	// SetText does not re-run the layout, so a label sized for "-" would stay clipped on GTK.
	changed := u.cpuVal.set(fmt.Sprintf("%.2f%% / %d00%%", cpu, u.info.NCPU))
	changed = u.cpuSub.set(fmt.Sprintf("(%d CPUs available)", u.info.NCPU)) || changed
	changed = u.memVal.set(formatBytes(mem)+" / "+formatBytes(u.info.MemTotal)) || changed
	if changed {
		u.pageViews[pageContainers].Unwrap().LayoutOverload2(true, true)
	}
	u.redrawChart()
	msg := fmt.Sprintf("Containers: RAM %s   CPU %.2f%%", formatBytes(mem), cpu/float64(max(u.info.NCPU, 1)))
	if u.failure != "" {
		msg += "   |   last action failed: " + u.failure
	}
	u.footL.set(msg)
	u.footR.set("Docker " + u.info.ServerVersion)
}

func (u *ui) applyError(err error) {
	if err.Error() != u.lastErr {
		u.lastErr = err.Error()
		log.Printf("engine error: %v", err)
	}
	if ab.noLabels {
		return
	}
	u.setEngine(engineDown)
	u.footL.set("Engine not reachable: " + err.Error())
}

func (u *ui) pushSelected() {
	for _, c := range u.all {
		if c.ID == u.selID {
			u.selHist.push(c.CPU)
			return
		}
	}
	u.selHist = nil
}

func (u *ui) setStatus(s string) { u.footL.set(s) }
