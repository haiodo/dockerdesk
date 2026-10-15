package main

import (
	"testing"

	"github.com/haiodo/gowt/swt"
)

func TestNavHistory(t *testing.T) {
	ctrs, imgs, logsA := place{page: pageContainers}, place{page: pageImages}, place{page: pageLogs, logID: "a"}
	h := newNavHistory(ctrs)
	check := func(step string, cur place, back, fwd bool) {
		t.Helper()
		if h.cur.Peek() != cur || h.canBack.Peek() != back || h.canFwd.Peek() != fwd {
			t.Fatalf("%s: at %+v back %v forward %v, want %+v %v %v", step, h.cur.Peek(), h.canBack.Peek(), h.canFwd.Peek(), cur, back, fwd)
		}
	}
	h.Back()
	check("back on an empty history", ctrs, false, false)
	h.Open(ctrs)
	check("open the current page", ctrs, false, false)
	h.Open(logsA)
	h.Open(imgs)
	check("two pages on", imgs, true, false)
	h.Back()
	check("back", logsA, true, true)
	h.Back()
	check("back to the start", ctrs, false, true)
	h.Forward()
	check("forward", logsA, true, true)
	h.Open(place{page: pageVolumes})
	check("open clears forward", place{page: pageVolumes}, true, false)
	h.Forward()
	check("forward with nothing ahead", place{page: pageVolumes}, true, false)
	h.Back()
	h.Back()
	check("back past the cleared branch", ctrs, false, true)
}

func TestNavKey(t *testing.T) {
	cases := []struct {
		goos      string
		mods, key int32
		want      int
	}{
		{"darwin", swt.COMMAND, '[', -1}, {"darwin", swt.COMMAND, ']', 1}, {"darwin", swt.ALT, swt.ARROW_LEFT, 0},
		{"darwin", swt.COMMAND | swt.SHIFT, '[', 0}, {"linux", swt.ALT, swt.ARROW_LEFT, -1}, {"windows", swt.ALT, swt.ARROW_RIGHT, 1},
		{"linux", 0, swt.ARROW_LEFT, 0}, {"linux", swt.CTRL, '[', 0},
	}
	for _, c := range cases {
		if got := navKey(c.goos, c.mods, c.key); got != c.want {
			t.Errorf("navKey(%s, %#x, %#x) = %d, want %d", c.goos, c.mods, c.key, got, c.want)
		}
	}
}
