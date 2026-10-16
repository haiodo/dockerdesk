package main

import (
	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/swt"
)

var (
	dockerBlue = gowt.RGB{R: 29, G: 99, B: 237}
	white      = gowt.RGB{R: 255, G: 255, B: 255}
)

type palette struct {
	content, sidebar, selected, footer gowt.RGB
	text, muted, green, blue           gowt.RGB
	grid, chartBG                      gowt.RGB
}

// paletteFor returns light or dark colours; native widgets (tree, tables, buttons) follow the
// system theme by themselves, the panels, labels and canvases drawn here follow this palette.
func paletteFor(dark bool) palette {
	if dark {
		return palette{
			content: gowt.RGB{R: 30, G: 31, B: 34}, sidebar: gowt.RGB{R: 38, G: 39, B: 43}, selected: gowt.RGB{R: 44, G: 62, B: 104}, footer: gowt.RGB{R: 38, G: 39, B: 43},
			text: gowt.RGB{R: 231, G: 233, B: 237}, muted: gowt.RGB{R: 150, G: 155, B: 164}, green: gowt.RGB{R: 84, G: 190, B: 140}, blue: gowt.RGB{R: 96, G: 150, B: 255},
			grid: gowt.RGB{R: 62, G: 64, B: 70}, chartBG: gowt.RGB{R: 30, G: 31, B: 34},
		}
	}
	return palette{
		content: white, sidebar: gowt.RGB{R: 244, G: 245, B: 247}, selected: gowt.RGB{R: 214, G: 227, B: 252}, footer: gowt.RGB{R: 244, G: 245, B: 247},
		text: gowt.RGB{R: 28, G: 32, B: 40}, muted: gowt.RGB{R: 100, G: 106, B: 118}, green: gowt.RGB{R: 52, G: 125, B: 92}, blue: dockerBlue,
		grid: gowt.RGB{R: 230, G: 232, B: 236}, chartBG: white,
	}
}

type surface int

const (
	surfNone surface = iota
	surfContent
	surfSidebar
	surfFooter
)

type ink int

const (
	inkNone ink = iota
	inkText
	inkMuted
	inkGreen
	inkBlue
)

func (p *palette) surfaceColor(s surface) gowt.RGB {
	switch s {
	case surfSidebar:
		return p.sidebar
	case surfFooter:
		return p.footer
	}
	return p.content
}

func (p *palette) inkColor(i ink) gowt.RGB {
	switch i {
	case inkMuted:
		return p.muted
	case inkGreen:
		return p.green
	case inkBlue:
		return p.blue
	}
	return p.text
}

// paint colours a widget from the palette, now and again after every theme change.
func (u *ui) paint(s surface, i ink) gowt.Option {
	return gowt.Init(func(w gowt.Widget) {
		gowt.Watch(w, u.pal, func(p palette) {
			if s != surfNone {
				w.SetBackground(p.surfaceColor(s))
			}
			if i != inkNone {
				w.SetForeground(p.inkColor(i))
			}
		})
	})
}

func (u *ui) onThemeChange(dark bool) {
	p := paletteFor(dark)
	u.pal.Set(p)
	u.navIcons.Set(u.loadNavIcons(p))
}

// fontSet holds the text styles as options; navHeading is the heading font for the painted sidebar.
type fontSet struct {
	title, heading, big, small gowt.Option
	navHeading                 gowt.Font
}

// initFonts derives sizes from the system font of ref instead of naming a family, so each OS keeps its own.
func (u *ui) initFonts(ref *swt.Control) {
	fd := ref.GetFont().GetFontData()[0]
	h := float64(fd.GetHeight())
	size := func(scale float64) int { return int(h*scale + 0.5) }
	font := func(scale float64, w gowt.Weight) gowt.Font { return gowt.Font{Size: size(scale), Weight: w} }
	mk := func(scale float64, w gowt.Weight) gowt.Option {
		f := font(scale, w)
		return gowt.Init(func(c gowt.Widget) { c.SetFont(f) })
	}
	u.fonts = fontSet{title: mk(1.15, gowt.Semibold), heading: mk(0.85, gowt.Semibold), big: mk(1.15, gowt.Medium), small: mk(0.85, gowt.Regular),
		navHeading: font(0.85, gowt.Semibold)}
}
