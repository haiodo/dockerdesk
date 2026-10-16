package main

import (
	"fmt"

	"github.com/haiodo/gowt"
	"github.com/haiodo/gowt/svg"
)

const navSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">%s</svg>`

// Navigation glyphs in the order of the sidebar pages.
var navShapes = []string{
	`<path d="M21 8l-9-5-9 5v8l9 5 9-5z"/><path d="M3 8l9 5 9-5M12 13v8"/>`,
	`<path d="M12 3l9 5-9 5-9-5z"/><path d="M3 13l9 5 9-5"/>`,
	`<rect x="3" y="12" width="18" height="8" rx="2"/><path d="M5 12l2-7h10l2 7"/>`,
	`<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>`,
}

const rowSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">%s</svg>`

// Tree glyphs keep fixed colours that read on both themes, so they are built once.
var rowShapes = map[string]string{
	"running": `<circle cx="8" cy="8" r="5" fill="#2E9F6B"/>`,
	"partial": `<circle cx="8" cy="8" r="4.6" fill="none" stroke="#1D63ED" stroke-width="1.4"/><path d="M8 3.4a4.6 4.6 0 0 1 0 9.2z" fill="#1D63ED"/>`,
	"stopped": `<circle cx="8" cy="8" r="4.6" fill="none" stroke="#8A8F98" stroke-width="1.4"/>`,
	"stop":    `<rect x="3.5" y="3.5" width="9" height="9" rx="1.5" fill="#1D63ED"/>`,
	"play":    `<path d="M5 3.5v9l7.5-4.5z" fill="none" stroke="#1D63ED" stroke-width="1.5" stroke-linejoin="round"/>`,
	"more":    `<circle cx="8" cy="3.5" r="1.3" fill="#6B7280"/><circle cx="8" cy="8" r="1.3" fill="#6B7280"/><circle cx="8" cy="12.5" r="1.3" fill="#6B7280"/>`,
	"down":    `<circle cx="8" cy="8" r="5" fill="#D64541"/>`,
	"wait":    `<circle cx="8" cy="8" r="5" fill="#8A8F98"/>`,
}

func (u *ui) loadRowIcons() {
	u.rowIcons = map[string]*gowt.Image{}
	for name, shape := range rowShapes {
		data := []byte(fmt.Sprintf(rowSVG, shape))
		u.rowIcons[name] = u.app.ImageFromProvider(svg.NewImageDataProvider(data, 16, 16, "#000000"))
	}
}

// loadNavIcons tints the sidebar glyphs with the text colour of p; called again on a theme change.
func (u *ui) loadNavIcons(p palette) []*gowt.Image {
	hex := fmt.Sprintf("#%02x%02x%02x", p.text.R, p.text.G, p.text.B)
	var icons []*gowt.Image
	for _, shape := range navShapes {
		data := []byte(fmt.Sprintf(navSVG, shape))
		icons = append(icons, u.app.ImageFromProvider(svg.NewImageDataProvider(data, 18, 18, hex)))
	}
	return icons
}
