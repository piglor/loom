package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

// canonicalJSON preserves the existing event identity contract: sorted keys,
// ASCII escapes, and optionally the retired writer's separator spacing. It is not
// a general canonicalization protocol for arbitrary floating-point payloads.
func canonicalJSON(v any, spaced bool) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var object any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err = decoder.Decode(&object); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(object); err != nil {
		return nil, err
	}
	var out strings.Builder
	quoted, escaped := false, false
	for _, r := range strings.TrimSuffix(buf.String(), "\n") {
		if r >= 127 {
			if r <= 0xffff {
				fmt.Fprintf(&out, "\\u%04x", r)
			} else {
				a, b := utf16.EncodeRune(r)
				fmt.Fprintf(&out, "\\u%04x\\u%04x", a, b)
			}
			continue
		}
		out.WriteRune(r)
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
		}
		if spaced && !quoted && (r == ',' || r == ':') {
			out.WriteByte(' ')
		}
	}
	return []byte(out.String()), nil
}
func hash(b []byte) string { digest := sha256.Sum256(b); return hex.EncodeToString(digest[:]) }
