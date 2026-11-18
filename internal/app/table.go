package app

import (
	"github.com/egoist/mygo/ui"
)

// This file is the only place in the app that builds a table.
//
// Onevra's components.Table does not scroll: it asks the caller to wrap it in a
// ScrollView, and the scroll view we have scrolls vertically. A column dragged
// wider than the pane therefore pushed its resize grip outside the clipped
// area, where no scroll could reach it. MyGo's ui.Table keeps the header and
// the rows in one sideways-scrolling area, so the grip follows its column.
//
// Pages call tableCard and nothing else: it frames the table, picks its height
// and names it for tests and assistive technology.

// tableOptions says how tableCard frames a table.
type tableOptions struct {
	// Label names the table for tests and assistive technology.
	Label string
	// Grow lets the card take the room its parent gives it: use it where the
	// table is the page's scroller.
	Grow bool
	// Rows is how many rows the card makes room for when Grow is false: the
	// table keeps its own scroll inside a page that scrolls around it.
	Rows int
	// Fit takes Rows at its word instead of capping at maxTableRows: use it
	// when a box around the table scrolls, so every row is drawn and no row is
	// hidden behind a second scrollbar inside the card. It also pins the rows
	// to the theme's least row height (ListState.RowHeight): only a uniform row
	// height lets the card be exactly as tall as what it shows. A row holding a
	// button is taller than that by its padding, so a card sized by an estimate
	// was short by a pixel or two per row — enough to hide the last row behind
	// the card's own scrollbar.
	Fit bool
	// Unframed omits the card's border, for a table already inside a frame.
	Unframed bool
}

// maxTableRows caps how many rows a fixed-height card makes room for.
const maxTableRows = 8

// tableCard builds the one table of a page and returns it, so the page can read
// Changed and Submitted; the card around it draws the border.
func tableCard(c *ui.Context, state *ui.ListState, columns []ui.TableColumn, rows int, opts tableOptions, cell func(row, col int)) ui.Element {
	theme := c.Theme()
	frame := ui.Column(c).FillWidth()
	// The 1 DIP padding keeps the table's own focus ring and the first column's
	// edge off the border, as the file browser's frame does.
	if !opts.Unframed {
		frame.Padding(1).Radius(theme.Radius).Border(1, theme.Border).Clip()
	}
	var table ui.Element
	frame.Children(func() {
		if opts.Fit {
			// tableRowsHeight counts the theme's least row for every row, which
			// is what the table has to give each one for the two to agree.
			state.RowHeight = c.Theme().Space(8)
		}
		table = ui.Table(c, state, columns, rows, cell).FillWidth().Label(opts.Label)
		if opts.Grow {
			table.Grow(1).MinHeight(0)
		} else {
			table.Height(tableRowsHeight(c, opts.Rows, opts.Fit))
		}
	})
	if opts.Grow {
		frame.Grow(1).MinHeight(0)
	}
	return table
}

// tableRowsHeight is how tall a table with rows rows is when it does not grow:
// the header plus the rows, at most maxTableRows of them unless fit asks for
// every row. Space(8) is the height of ui.Table's own header and its least row.
func tableRowsHeight(c *ui.Context, rows int, fit bool) float32 {
	shown := max(rows, 1)
	if !fit {
		shown = min(shown, maxTableRows)
	}
	return c.Theme().Space(8) * float32(shown+1)
}
