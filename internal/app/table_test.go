package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTableCardSizesAShortTableForItsRows pins the one height rule a card with
// rows but no room to grow follows: the table is as tall as its header plus its
// rows, so a table inside a page that scrolls does not swallow the page.
func TestTableCardSizesAShortTableForItsRows(t *testing.T) {
	var state ui.ListState
	var want float32
	tt := ui.NewTester(func(c *ui.Context) {
		want = tableRowsHeight(c, 3)
		tableCard(c, &state, []ui.TableColumn{{Title: "名称", ID: "name"}}, 3,
			tableOptions{Label: "探针表", Rows: 3},
			func(row, col int) { ui.Text(c, "行"+itoa(row)) })
	}, 600, 400)
	rect, ok := tt.Find("探针表")
	if !ok {
		t.Fatalf("the card's table is not labelled; texts = %v", tt.Texts())
	}
	if rect.H != want {
		t.Fatalf("table height = %v, want %v", rect.H, want)
	}
}

// TestTableCardCapsHowManyRowsItMakesRoomFor keeps a long list from pushing a
// page past the window: past maxTableRows the table scrolls its own rows.
func TestTableCardCapsHowManyRowsItMakesRoomFor(t *testing.T) {
	var state ui.ListState
	var want float32
	tt := ui.NewTester(func(c *ui.Context) {
		want = tableRowsHeight(c, maxTableRows)
		tableCard(c, &state, []ui.TableColumn{{Title: "名称", ID: "name"}}, 40,
			tableOptions{Label: "长表", Rows: 40},
			func(row, col int) { ui.Text(c, "行"+itoa(row)) })
	}, 600, 400)
	rect, ok := tt.Find("长表")
	if !ok {
		t.Fatalf("the card's table is not labelled; texts = %v", tt.Texts())
	}
	if rect.H != want {
		t.Fatalf("table height = %v, want the %d-row cap %v", rect.H, maxTableRows, want)
	}
}

// TestOnlyFilesLeavesTheTableUnframed pins the single exception to the framing
// rule: the file browser's pane is already a 1 DIP frame, so its table must not
// draw a second one.
func TestOnlyFilesLeavesTheTableUnframed(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(source), "Unframed: true") && path != "files.go" {
			t.Fatalf("%s leaves its table unframed; only the file browser's pane already frames it", path)
		}
	}
}

// TestTablesAreBuiltOnlyByTableCard keeps the framing, height and label rules
// in one place: a page that writes ui.Table itself looks like the others but
// silently loses the border, the height cap and the label.
func TestTablesAreBuiltOnlyByTableCard(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "table.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(source), "ui.Table(") {
			t.Fatalf("%s builds a table itself; build it with tableCard", path)
		}
	}
}
