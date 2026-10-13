package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/haiodo/gowt"
)

const actionWorkers = 8

// act runs verb on the selected row; a group row means its visible members.
func (u *ui) act(verb string) {
	k, ok := u.selectedKey()
	if !ok {
		return
	}
	u.actOn(verb, targetsOf(u.visible, k), k.id == "")
}

// actOn confirms, then runs the engine calls off the UI thread. Everything but a start of one
// container asks first, because stop and restart interrupt running work.
func (u *ui) actOn(verb string, targets []*Ctr, group bool) {
	targets = relevant(verb, targets)
	if len(targets) == 0 {
		return
	}
	title := strings.ToUpper(verb[:1]) + verb[1:]
	if (verb != "start" || group) && !u.win.Confirm(title, fmt.Sprintf("%s %s?", title, describe(targets))) {
		return
	}
	u.failure = ""
	u.setStatus(fmt.Sprintf("%s %s...", title, describe(targets)))
	go func() {
		errs := make([]error, len(targets))
		sem := make(chan struct{}, actionWorkers)
		var wg sync.WaitGroup
		for i, c := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := u.eng.Action(u.ctx, c.ID, verb); err != nil {
					errs[i] = fmt.Errorf("%s: %w", c.Name(), err)
				}
			}()
		}
		wg.Wait()
		ids := make([]string, len(targets))
		for i, c := range targets {
			ids[i] = c.ID
		}
		u.poll.Refresh(ids...)
		u.app.Async(func() { u.actionDone(verb, errors.Join(errs...)) })
	}()
}

func (u *ui) actionDone(verb string, err error) {
	if err == nil {
		return
	}
	u.failure = verb + " failed"
	u.setStatus("Action failed: " + verb)
	u.win.MessageBox(gowt.Message{Title: "Docker", Text: fmt.Sprintf("%s failed:\n%v", verb, err), Icon: gowt.IconError, Buttons: gowt.ButtonsOK})
}

// relevant keeps the containers the verb applies to: start only stopped ones, stop and restart
// only running ones, so a group restart never starts containers that were stopped on purpose.
func relevant(verb string, cs []*Ctr) []*Ctr {
	var out []*Ctr
	for _, c := range cs {
		if (verb == "start") != c.Running() {
			out = append(out, c)
		}
	}
	return out
}

// describe names up to three containers so the dialog shows what is affected.
func describe(cs []*Ctr) string {
	if len(cs) == 1 {
		return "container " + cs[0].Name()
	}
	var names []string
	for _, c := range cs[:min(3, len(cs))] {
		names = append(names, c.Name())
	}
	s := fmt.Sprintf("%d containers (%s", len(cs), strings.Join(names, ", "))
	if len(cs) > 3 {
		s += fmt.Sprintf(" and %d more", len(cs)-3)
	}
	return s + ")"
}
