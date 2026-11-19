package app

import "testing"

// The tray image has to follow the bar: dark ink on a light bar, light ink on
// a dark one. Picking the wrong member is how an icon disappears on a dark
// taskbar, and no view test can see it.
func TestTrayIconsFollowTheBar(t *testing.T) {
	icons := TrayIcons{Light: []byte("dark-ink"), Dark: []byte("light-ink")}
	if got := string(icons.forBar(false)); got != "dark-ink" {
		t.Fatalf("forBar(false) = %q, want the dark-ink image for a light bar", got)
	}
	if got := string(icons.forBar(true)); got != "light-ink" {
		t.Fatalf("forBar(true) = %q, want the light-ink image for a dark bar", got)
	}
}
