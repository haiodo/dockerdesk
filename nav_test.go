package main

import "testing"

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

func TestNavKeys(t *testing.T) {
	cases := []struct{ goos, back, fwd string }{
		{"darwin", "Cmd+[", "Cmd+]"}, {"linux", "Alt+Left", "Alt+Right"}, {"windows", "Alt+Left", "Alt+Right"},
	}
	for _, c := range cases {
		if b, f := navKeys(c.goos); b != c.back || f != c.fwd {
			t.Errorf("navKeys(%s) = %q, %q, want %q, %q", c.goos, b, f, c.back, c.fwd)
		}
	}
}
