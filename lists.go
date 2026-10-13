package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/haiodo/gowt"
)

// listPage is a read-only table filled by load on a worker goroutine whenever its page is opened.
type listPage struct {
	table *gowt.Table
	load  func(ctx context.Context) ([][]string, error)
}

func (u *ui) newListPage(p *gowt.Panel, title string, cols []string, widths []int, load func(context.Context) ([][]string, error)) *listPage {
	u.label(p, title, surfContent, inkText, u.fonts.title)
	t := p.Table(fillBoth)
	t.ShowHeader(true)
	for i, c := range cols {
		t.Column(c, widths[i])
	}
	return &listPage{t, load}
}

func (u *ui) reload(lp *listPage) {
	go func() {
		rows, err := lp.load(u.ctx)
		u.app.Async(func() {
			if err != nil {
				u.setStatus("Cannot read list: " + err.Error())
				return
			}
			lp.table.Clear()
			for _, r := range rows {
				lp.table.Row(r...)
			}
		})
	}()
}

func (u *ui) imageRows(ctx context.Context) ([][]string, error) {
	imgs, err := u.eng.Images(ctx)
	var rows [][]string
	for _, im := range imgs {
		tag := "<none>"
		if len(im.RepoTags) > 0 {
			tag = strings.Join(im.RepoTags, ", ")
		}
		id := strings.TrimPrefix(im.ID, "sha256:")
		rows = append(rows, []string{tag, id[:min(12, len(id))], formatBytes(im.Size), formatAgo(time.Unix(im.Created, 0), time.Now())})
	}
	return rows, err
}

func (u *ui) volumeRows(ctx context.Context) ([][]string, error) {
	vols, err := u.eng.Volumes(ctx)
	var rows [][]string
	for _, v := range vols {
		rows = append(rows, []string{v.Name, v.Driver, v.Mountpoint})
	}
	return rows, err
}

func summaryText(info Info, cpu float64, mem int64) (cpuLine, memLine string) {
	cpuLine = fmt.Sprintf("Container CPU usage %.2f%% / %d00%% (%d CPUs)", cpu, info.NCPU, info.NCPU)
	memLine = fmt.Sprintf("Container memory usage %s / %s", formatBytes(mem), formatBytes(info.MemTotal))
	return cpuLine, memLine
}
