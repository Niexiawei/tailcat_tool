package core

import (
	"strings"
	"testing"
)

const sampleAddress = "tc" + "AbC123_-xyz789" + "0123456789abcdef"

func TestAddressPlausible(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    bool
	}{
		{"minimum length", "tc" + strings.Repeat("A", 20), true},
		{"typical build address", sampleAddress, true},
		{"too short", "tc" + strings.Repeat("A", 19), false},
		{"empty", "", false},
		{"missing prefix", strings.Repeat("A", 30), false},
		{"wrong case prefix", "TC" + strings.Repeat("A", 20), false},
		{"internal space", "tc" + strings.Repeat("A", 10) + " " + strings.Repeat("A", 10), false},
		{"trailing newline", "tc" + strings.Repeat("A", 20) + "\n", false},
		{"pasted with leading space", " tc" + strings.Repeat("A", 20), false},
		{"over the 8 KiB ceiling", "tc" + strings.Repeat("A", 8192), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Plausible(tc.address); got != tc.want {
				t.Fatalf("Plausible(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}

func TestRedactAddressAndPattern(t *testing.T) {
	const other = "tc" + "ZZZ999_-qrs456" + "fedcba9876543210"
	cases := []struct {
		name    string
		text    string
		address string
		want    string
	}{
		{
			name:    "known address is replaced verbatim",
			text:    "connecting to " + sampleAddress + " now",
			address: sampleAddress,
			want:    "connecting to [连接凭据已隐藏] now",
		},
		{
			name:    "unknown address matched by pattern",
			text:    "peer " + other + " replied",
			address: sampleAddress,
			want:    "peer [连接凭据已隐藏] replied",
		},
		{
			name:    "no address configured still redacts",
			text:    "only " + other + " here",
			address: "",
			want:    "only [连接凭据已隐藏] here",
		},
		{
			name:    "text without credentials is untouched",
			text:    "adb device online",
			address: sampleAddress,
			want:    "adb device online",
		},
		{
			name:    "two occurrences both replaced",
			text:    sampleAddress + " -> " + other,
			address: sampleAddress,
			want:    "[连接凭据已隐藏] -> [连接凭据已隐藏]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.text, tc.address); got != tc.want {
				t.Fatalf("Redact(%q, %q) = %q, want %q", tc.text, tc.address, got, tc.want)
			}
		})
	}
}

// A credential split across pipe reads must never reach the caller in pieces:
// the redactor buffers partial lines and only emits whole lines.
func TestOutputRedactorSplitCredential(t *testing.T) {
	r := NewRedactor(sampleAddress)
	chunks := []string{
		"forwarding " + sampleAddress[:12],
		sampleAddress[12:24],
		sampleAddress[24:] + " ready\n",
	}
	var emitted strings.Builder
	for i, chunk := range chunks {
		out := r.Append(chunk)
		if strings.Contains(out, "tc") {
			t.Fatalf("chunk %d emitted a credential fragment: %q", i, out)
		}
		emitted.WriteString(out)
	}
	out := r.Append("done\n")
	emitted.WriteString(out)
	if got := emitted.String(); got != "forwarding [连接凭据已隐藏] ready\ndone\n" {
		t.Fatalf("emitted %q", got)
	}
	if tail := r.Flush(); tail != "" {
		t.Fatalf("Flush() = %q, want empty", tail)
	}
}

func TestOutputRedactorLongLine(t *testing.T) {
	r := NewRedactor("")
	out := r.Append(strings.Repeat("x", 32769))
	if !strings.Contains(out, MsgLineTruncated) {
		t.Fatalf("expected truncation notice, got %q", out)
	}
	// Everything up to the next newline belongs to the over-long line, so it is
	// dropped rather than shown as a fragment; the line after it is fine.
	if got := r.Append("tail of the long line\nnext\n"); got != "next\n" {
		t.Fatalf("redactor did not recover after the long line's newline: %q", got)
	}
}

func TestOutputRedactorFlushEmitsPartialLine(t *testing.T) {
	r := NewRedactor(sampleAddress)
	if out := r.Append("partial " + sampleAddress); out != "" {
		t.Fatalf("partial line should be buffered, got %q", out)
	}
	if got := r.Flush(); got != "partial [连接凭据已隐藏]" {
		t.Fatalf("Flush() = %q", got)
	}
	if got := r.Flush(); got != "" {
		t.Fatalf("second Flush() = %q, want empty", got)
	}
}

func TestOutputRedactorCRLF(t *testing.T) {
	r := NewRedactor(sampleAddress)
	if got := r.Append("a " + sampleAddress + "\r\nb\r\n"); got != "a [连接凭据已隐藏]\r\nb\r\n" {
		t.Fatalf("got %q", got)
	}
}
