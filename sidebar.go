package main

import (
	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/swt"
)

var navTitles = []string{"Containers", "Images", "Volumes", "Logs"}

const (
	navTop   = 16
	navRowH  = 38
	navInset = 12
	navIcon  = 18
)

// navRect is the rectangle of item i; the painter and the click handler both use it.
func navRect(i, width int) (x, y, w, h int) {
	return navInset, navTop + i*navRowH, width - 2*navInset, navRowH - 4
}

func (u *ui) buildSidebar(root *gowt.Panel) {
	u.nav = root.Canvas(u.paintNav, gowt.Cell(gowt.GridCell{Align: gowt.AlignFill, VAlign: gowt.AlignFill, GrowY: true, Width: 230}))
	u.nav.Unwrap().AddMouseListener(swt.MouseListenerMouseDownAdapter(u.onNavClick))
}

func (u *ui) paintNav(g *gowt.GC) {
	p := &u.pal
	w, h := u.nav.Size()
	g.SetFill(p.sidebar)
	g.FillRect(0, 0, w, h)
	for i, title := range navTitles {
		x, y, rw, rh := navRect(i, w)
		bg := p.sidebar
		if i == u.page {
			bg = p.selected
		}
		g.SetFill(bg)
		g.FillRect(x, y, rw, rh)
		g.Image(u.navIcons[i], x+12, y+(rh-navIcon)/2)
		g.SetColor(p.text)
		_, th := g.TextSize(title)
		g.Text(title, x+12+navIcon+12, y+(rh-th)/2)
	}
	u.paintEngine(g, h)
}

// paintEngine draws the engine state at the bottom of the sidebar.
func (u *ui) paintEngine(g *gowt.GC, h int) {
	p := &u.pal
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
func (u *ui) onNavClick(e *swt.MouseEvent) {
	if e.Button != 1 || e.Count != 1 {
		return
	}
	w, _ := u.nav.Size()
	for i := range navTitles {
		x, y, rw, rh := navRect(i, w)
		if int(e.X) >= x && int(e.X) < x+rw && int(e.Y) >= y && int(e.Y) < y+rh {
			u.openPage(i)
			return
		}
	}
}
