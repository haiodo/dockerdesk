package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/rx"
)

const (
	maxLogLines = 3000
	logFlush    = 250 * time.Millisecond
)

// traceLogs (DOCKERDESK_TRACE=1) prints timings of the log pipeline to stderr.
var traceLogs = os.Getenv("DOCKERDESK_TRACE") == "1"

var tailChoices = []string{"100", "500", "1000"}

// logStream is the buffer of one running log request; replacing it drops late lines of the old request.
type logStream struct {
	mu      sync.Mutex
	pending []string
	start   time.Time
	got     int
}

func (s *logStream) add(stream byte, line string) {
	line = shortStamp(line)
	if stream == 2 {
		line = "[err] " + line
	}
	s.mu.Lock()
	s.pending = append(s.pending, line)
	if traceLogs {
		if s.got == 0 {
			log.Printf("logs: first line after %v", time.Since(s.start))
		}
		s.got++
	}
	s.mu.Unlock()
}

func (s *logStream) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = nil
	return p
}

// shortStamp turns "2024-05-01T12:30:45.123456789Z msg" into "12:30:45 msg".
func shortStamp(line string) string {
	if len(line) > 20 && line[10] == 'T' {
		if i := strings.IndexByte(line, ' '); i > 19 {
			return line[11:19] + line[i:]
		}
	}
	return line
}

func (u *ui) buildLogs(p *gowt.Panel) {
	u.label(p, "Logs", surfContent, inkText, u.fonts.title)
	bar := u.panel(p, surfContent, gowt.Row{Spacing: gowt.Spacing.SM, Center: true}, fillX)
	title := bar.Label("", gowt.InRow(gowt.RowCell{Width: 360}))
	u.style(&title.Unwrap().Control, surfContent, inkText)
	title.BindText(rx.Compute(func() string {
		id := u.logID.Get()
		if id == "" {
			return "Select a container and use View logs"
		}
		for _, c := range u.ctrs.Get() {
			if c.ID == id {
				return "Logs: " + c.Name()
			}
		}
		return "Logs: "
	}))
	u.label(bar, "Tail", surfContent, inkText, nil)
	tail := bar.Combo(tailChoices, gowt.ReadOnly())
	tail.Select(u.tail.Peek())
	tail.OnSelect(u.tail.Set)
	follow := bar.Button("Follow", nil, gowt.Check())
	follow.BindChecked(u.follow)
	u.style(&follow.Unwrap().Control, surfNone, inkNone) // native colours: the system theme draws the check box
	u.logText = p.StyledText(gowt.Scrollbars(), fillBoth)
	u.logText.SetEditable(false)
	u.logText.SetWrap(false)
	rx.Effect(u.streamLogs)
}

// showLogs opens the Logs page for the selected container and starts the stream.
func (u *ui) showLogs() {
	k, ok := u.selectedKey()
	if !ok || k.id == "" {
		return
	}
	u.logID.Set(k.id)
	u.openPage(pageLogs)
}

// streamLogs is an effect: a new container, tail length or Follow restarts the stream, and the
// cleanup cancels the previous one.
func (u *ui) streamLogs() {
	id, tailIdx, follow := u.logID.Get(), u.tail.Get(), u.follow.Get()
	if id == "" {
		return
	}
	u.logText.SetText("")
	u.logCount = 0
	tail, _ := strconv.Atoi(tailChoices[max(tailIdx, 0)])
	ctx, cancel := context.WithCancel(u.ctx)
	rx.OnCleanup(cancel)
	s := &logStream{start: time.Now()}
	u.logCur = s
	go func() {
		err := u.eng.Logs(ctx, id, tail, follow, s.add)
		if traceLogs {
			log.Printf("logs: stream ended after %v, %d lines", time.Since(s.start), s.got)
		}
		if err != nil && ctx.Err() == nil {
			s.add(2, "-- log stream ended: "+err.Error())
		}
	}()
}

// flushLogs moves buffered lines into the text widget a few times a second instead of once per line.
func (u *ui) flushLogs() {
	if u.logCur != nil {
		if lines := u.logCur.take(); len(lines) > 0 {
			u.appendLogs(lines)
		}
	}
	u.app.After(logFlush, u.flushLogs)
}

func (u *ui) appendLogs(lines []string) {
	t0, total := time.Now(), len(lines)
	u.logText.Append(strings.Join(lines, "\n") + "\n")
	t1 := time.Now()
	u.logCount += total
	t2 := t1
	if excess := u.logCount - maxLogLines; excess > 0 {
		// Cut the head in place so scroll position and selection survive.
		st := u.logText.Unwrap()
		st.ReplaceTextRange(0, st.GetOffsetAtLine(int32(excess)), "")
		u.logCount = maxLogLines
		t2 = time.Now()
	}
	t3 := time.Now()
	if u.follow.Peek() {
		u.logText.SetTopLine(u.logText.Lines() - 1)
	}
	if traceLogs {
		log.Printf("logs: append %d lines: Append=%v trim=%v SetTopLine=%v total=%v since-open=%v",
			total, t1.Sub(t0), t2.Sub(t1), time.Since(t3), time.Since(t0), time.Since(u.logCur.start))
	}
}
