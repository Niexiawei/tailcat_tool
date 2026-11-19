package main

import (
	"bytes"
	"image/png"
	"testing"
)

// The window and the taskbar take their icon from this PNG at run time, so it
// has to be a real 1024×1024 square: an empty or wrong-sized file shows as a
// blank taskbar button, which no other test would notice.
func TestBundledAppIconIsASquarePNG(t *testing.T) {
	if len(appIcon) == 0 {
		t.Fatal("the application icon was not embedded")
	}
	img, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		t.Fatalf("the application icon is not a PNG: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 1024 || bounds.Dy() != 1024 {
		t.Fatalf("icon is %dx%d, want 1024x1024", bounds.Dx(), bounds.Dy())
	}
}
