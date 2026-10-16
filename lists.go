package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/haiodo/gowt"
)

// listPage is a read-only table filled by load on a worker goroutine whenever its page is opened.
func (u *ui) listPage(page int, title string, cols []string, widths []int, load func(context.Context) ([][]string, error)) gowt.Option {
	return u.pagePanel(
		u.title(title),
		gowt.Table(gowt.Grow(), gowt.Init(func(t *gowt.TableView) {
			t.ShowHeader(true)
			for i, c := range cols {
				t.Column(c, widths[i])
			}
			gowt.Watch(t, u.page, func(p int) {
				if p == page {
					u.reload(t, load)
				}
			})
		})))
}

func (u *ui) reload(t *gowt.TableView, load func(context.Context) ([][]string, error)) {
	go func() {
		rows, err := load(u.ctx)
		u.app.Async(func() {
			if err != nil {
				u.status.Set("Cannot read list: " + err.Error())
				return
			}
			t.Batch(func() {
				t.Clear()
				for _, r := range rows {
					t.Row(r...)
				}
			})
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
