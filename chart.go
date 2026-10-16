package main

import (
	"fmt"

	"github.com/haiodo/gowt"
)

const historyLen = 60

// history is a fixed window of the last samples, oldest first.
type history []float64

func (h *history) push(v float64) {
	if len(*h) == historyLen {
		copy(*h, (*h)[1:])
		*h = (*h)[:historyLen-1]
	}
	*h = append(*h, v)
}

// niceMax is the smallest of 1, 2 or 5 times a power of ten that is not below peak (at least 10).
func niceMax(peak float64) float64 {
	for base := 1.0; ; base *= 10 {
		for _, f := range []float64{1, 2, 5} {
			if v := f * base; v >= 10 && v >= peak {
				return v
			}
		}
	}
}

func (u *ui) redrawChart() {
	if u.chartOn.Peek() {
		u.chart.Redraw()
	}
}

// paintChart draws both series spread over the full width; the callback owns the whole canvas every time.
func (u *ui) paintChart(g *gowt.GC) {
	p := u.pal.Peek()
	w, h := u.chart.Size()
	const left, top, bottom, right = 46, 22, 8, 12
	pw, ph := w-left-right, h-top-bottom
	g.SetFill(p.chartBG)
	g.FillRect(0, 0, w, h)
	if pw < 10 || ph < 10 {
		return
	}
	peak := 0.0
	for _, s := range [][]float64{u.totalHist, u.selHist} {
		for _, v := range s {
			peak = max(peak, v)
		}
	}
	ymax := niceMax(peak)
	y := func(v float64) int { return top + ph - int(v/ymax*float64(ph)) }
	g.SetLineWidth(1)
	for _, f := range []float64{0, 0.5, 1} {
		g.SetColor(p.grid)
		g.Line(left, y(ymax*f), left+pw, y(ymax*f))
		g.SetColor(p.muted)
		g.Text(fmt.Sprintf("%g%%", ymax*f), 6, y(ymax*f)-7)
	}
	plot(g, u.totalHist, p.blue, left, pw, y)
	plot(g, u.selHist, gowt.RGB{R: 230, G: 120, B: 20}, left, pw, y)
	g.SetColor(p.blue)
	g.Text("total CPU", left, 3)
	if u.selName != "" {
		g.SetColor(gowt.RGB{R: 230, G: 120, B: 20})
		g.Text(u.selName, left+90, 3)
	}
}

// plot spreads the samples over the whole width, oldest at the left edge.
func plot(g *gowt.GC, h history, col gowt.RGB, left, pw int, y func(float64) int) {
	if len(h) < 2 {
		return
	}
	g.SetColor(col)
	g.SetLineWidth(2)
	x := func(i int) int { return left + i*pw/(len(h)-1) }
	for i := 1; i < len(h); i++ {
		g.Line(x(i-1), y(h[i-1]), x(i), y(h[i]))
	}
}
