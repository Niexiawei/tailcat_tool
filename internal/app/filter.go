package app

import (
	"strings"

	"github.com/Niexiawei/tailcat-tool/internal/core"
)

// matchQuery reports whether any of the fields answers the whole query. The
// query is split on spaces and every term must hit at least one field; a term
// matches case-insensitively and fuzzily, so the characters may be spread
// through the field ("mp" finds "com.google.android.apps.maps"). A process list
// with 121 entries is not something to scroll through by hand.
func matchQuery(query string, fields ...string) bool {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		hit := false
		for _, field := range fields {
			if fuzzyContains(strings.ToLower(field), term) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// fuzzyContains takes needle's characters out of haystack in order.
func fuzzyContains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(haystack, needle) {
		return true
	}
	want := []rune(needle)
	at := 0
	for _, r := range haystack {
		if r == want[at] {
			at++
			if at == len(want) {
				return true
			}
		}
	}
	return false
}

// filteredApp is one application row after a query: whether the application
// itself matched, and which of its processes did.
type filteredApp struct {
	app       core.AppUsage
	processes []core.ProcessUsage
	// full reports that the application matched by its own name or package, so
	// every process under it is interesting; otherwise only processes is.
	full bool
}

// filterApps keeps the applications a query asks for. A group survives when it
// matches itself or when one of its processes does, which is how a user finds
// com.foo:remote without expanding 121 rows first.
func filterApps(apps []core.AppUsage, query string) []filteredApp {
	kept := make([]filteredApp, 0, len(apps))
	for _, app := range apps {
		full := matchQuery(query, appLabel(app), app.Package)
		var processes []core.ProcessUsage
		for _, process := range app.Processes {
			if matchQuery(query, process.Name) {
				processes = append(processes, process)
			}
		}
		if !full && len(processes) == 0 {
			continue
		}
		kept = append(kept, filteredApp{app: app, processes: processes, full: full})
	}
	return kept
}

// filterProcessRows keeps the processes a query asks for, by their own name or
// by the application they belong to.
func filterProcessRows(rows []processRow, query string) []processRow {
	kept := make([]processRow, 0, len(rows))
	for _, row := range rows {
		if !matchQuery(query, row.process.Name, row.app, row.pkg) {
			continue
		}
		kept = append(kept, row)
	}
	return kept
}
