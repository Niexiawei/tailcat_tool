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
		table = ui.Table(c, state, columns, rows, cell).FillWidth().Label(opts.Label)
		if opts.Grow {
			table.Grow(1).MinHeight(0)
		} else {
			table.Height(tableRowsHeight(c, opts.Rows))
		}
	})
	if opts.Grow {
		frame.Grow(1).MinHeight(0)
	}
	return table
}

// tableRowsHeight is how tall a table with rows rows is when it does not grow:
// the header plus the rows, at most maxTableRows of them. Space(8) is the height
// of ui.Table's own header and its least row.
func tableRowsHeight(c *ui.Context, rows int) float32 {
	shown := min(max(rows, 1), maxTableRows)
	return c.Theme().Space(8) * float32(shown+1)
}
