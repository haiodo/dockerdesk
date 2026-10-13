package main

import "testing"

func TestHistoryKeepsLastSamples(t *testing.T) {
	var h history
	for i := 0; i < historyLen+5; i++ {
		h.push(float64(i))
	}
	if len(h) != historyLen || h[0] != 5 || h[historyLen-1] != float64(historyLen+4) {
		t.Fatalf("len %d first %v last %v", len(h), h[0], h[len(h)-1])
	}
}

func TestNiceMax(t *testing.T) {
	for in, want := range map[float64]float64{0: 10, 7: 10, 22: 50, 100: 100, 101: 200, 350: 500, 1200: 2000} {
		if got := niceMax(in); got != want {
			t.Errorf("niceMax(%v)=%v want %v", in, got, want)
		}
	}
}
