package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// TestTableCardSizesAShortTableForItsRows pins the one height rule a card with
// rows but no room to grow follows: the table is as tall as its header plus its
// rows, so a table inside a page that scrolls does not swallow the page.
func TestTableCardSizesAShortTableForItsRows(t *testing.T) {
	var state ui.ListState
	var want float32
	tt := ui.NewTester(func(c *ui.Context) {
		want = tableRowsHeight(c, 3, false)
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
		want = tableRowsHeight(c, maxTableRows, false)
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

// TestTableCardFitsEveryRowWhenAsked pins the option the DERP list needs: the
// card takes rows past the cap, because there the box around the table is what
// scrolls, and a capped card would hide rows behind a second scrollbar.
func TestTableCardFitsEveryRowWhenAsked(t *testing.T) {
	var state ui.ListState
	var want, cap float32
	tt := ui.NewTester(func(c *ui.Context) {
		want = tableRowsHeight(c, 40, true)
		cap = tableRowsHeight(c, maxTableRows, false)
		tableCard(c, &state, []ui.TableColumn{{Title: "名称", ID: "name"}}, 40,
			tableOptions{Label: "整表", Rows: 40, Fit: true},
			func(row, col int) { ui.Text(c, "行"+itoa(row)) })
	}, 600, 1600)
	rect, ok := tt.Find("整表")
	if !ok {
		t.Fatalf("the card's table is not labelled; texts = %v", tt.Texts())
	}
	if rect.H != want {
		t.Fatalf("table height = %v, want all 40 rows (%v)", rect.H, want)
	}
	if want <= cap {
		t.Fatalf("the fit height %v is not past the %d-row cap %v", want, maxTableRows, cap)
	}
	// A card can only be exactly as tall as its rows when every row is the
	// same height, so Fit pins it rather than trusting the content.
	if state.RowHeight <= 0 {
		t.Fatalf("Fit left RowHeight at %v, so the height is an estimate", state.RowHeight)
	}
}

// unframedTables maps the tables that may leave the card's border off to the
// frame the page already draws around them. A table inside a frame of its own
// would read as a box in a box.
var unframedTables = map[string]string{
	"files.go": "the file browser's right pane is the frame",
	"derp.go":  "the preset list sits in the box that scrolls it",
}

// TestOnlyAFramedPaneLeavesTheTableUnframed pins the single exception to the
// framing rule: a table already inside a frame the page draws (the file
// browser's pane, the DERP list's scroll box) must not draw a second one.
func TestOnlyAFramedPaneLeavesTheTableUnframed(t *testing.T) {
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
		if strings.Contains(string(source), "Unframed: true") {
			if _, ok := unframedTables[path]; !ok {
				t.Fatalf("%s leaves its table unframed; only a table already inside a frame may", path)
			}
			continue
		}
		// A table that stopped being unframed left a stale entry behind.
		if reason, ok := unframedTables[path]; ok {
			t.Fatalf("%s no longer leaves a table unframed (%s)", path, reason)
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

// TestBadgesSitCenteredInATableCell pins what the 类型 column looked like on a
// real screen: components.Badge aligns itself to the start of a row, so in a
// table cell (which centres its content) the pill rode above the name it
// describes. Before the fix the two centres were about six DIP apart.
func TestBadgesSitCenteredInATableCell(t *testing.T) {
	vm := probeDerpViewModel(t, &listProber{}, nil, core.SaveRelayPresetRequest{
		Name: "自建2", Source: "https://relay.example.com/map.json",
	})
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	badge, ok := tt.Find("链接")
	if !ok {
		t.Fatalf("no 链接 badge; texts = %v", tt.Texts())
	}
	name, ok := tt.Find("自建2")
	if !ok {
		t.Fatalf("no 自建2 row; texts = %v", tt.Texts())
	}
	badgeCenter := badge.Y + badge.H/2
	nameCenter := name.Y + name.H/2
	if diff := badgeCenter - nameCenter; diff > 1 || diff < -1 {
		t.Fatalf("the badge sits %.1f DIP off the name (badge centre %.1f, name centre %.1f)", diff, badgeCenter, nameCenter)
	}
}
