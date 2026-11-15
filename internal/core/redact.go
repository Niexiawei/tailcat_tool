package core

import (
	"regexp"
	"strings"
)

// maxAddressLength is the longest string Plausible accepts. The Swift build
// used the same ceiling; the credential store applies its own, smaller limit.
const maxAddressLength = 8192

// maxPendingLine is where the redactor gives up on a line that never ends.
const maxPendingLine = 32768

var (
	addressPattern  = regexp.MustCompile(`^tc[A-Za-z0-9_-]{20,}$`)
	freeformPattern = regexp.MustCompile(`tc[A-Za-z0-9_-]{20,}`)
)

// Plausible reports whether address can be a complete Tailcat connection
// address: "tc" followed by at least 20 URL-safe characters, at most 8 KiB.
func Plausible(address string) bool {
	return len(address) <= maxAddressLength && addressPattern.MatchString(address)
}

// Redact removes address (matched verbatim) and every other tc... token from
// text, replacing each with MsgRedacted.
func Redact(text, address string) string {
	out := text
	if address != "" {
		out = strings.ReplaceAll(out, address, MsgRedacted)
	}
	return freeformPattern.ReplaceAllLiteralString(out, MsgRedacted)
}

// Redactor buffers incomplete lines so a credential split across two reads
// never reaches the UI. Append returns only whole, redacted lines; Flush
// returns whatever partial line is left.
type Redactor struct {
	pending    string
	discarding bool
	address    string
}

// NewRedactor returns a redactor that also scrubs address verbatim.
func NewRedactor(address string) *Redactor {
	return &Redactor{address: address}
}

// Append feeds one chunk of process output and returns the redacted lines that
// became complete, each terminated by "\n".
func (r *Redactor) Append(chunk string) string {
	r.pending += chunk
	var out strings.Builder
	for {
		idx := strings.IndexByte(r.pending, '\n')
		if idx < 0 {
			break
		}
		line := r.pending[:idx]
		r.pending = r.pending[idx+1:]
		if !r.discarding {
			out.WriteString(Redact(line, r.address))
			out.WriteByte('\n')
		}
		r.discarding = false
	}
	if len(r.pending) > maxPendingLine {
		r.pending = ""
		r.discarding = true
		out.WriteString(MsgLineTruncated)
		out.WriteByte('\n')
	}
	return out.String()
}

// Flush returns the buffered partial line, redacted, and resets the redactor.
func (r *Redactor) Flush() string {
	pending, discarding := r.pending, r.discarding
	r.pending, r.discarding = "", false
	if discarding {
		return ""
	}
	return Redact(pending, r.address)
}
