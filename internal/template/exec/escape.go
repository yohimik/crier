package exec

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// escapeHTML is html/template's escaping for text content: the five
// characters HTML gives meaning to, the plus sign (which a browser can read
// as a space in some places), and NUL as the replacement character. It is
// what every action's output goes through under the HTML flag.
func escapeHTML(s string) string {
	if !strings.ContainsAny(s, "\"&'+<>\000") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\000':
			b.WriteString("�")
		case '"':
			b.WriteString("&#34;")
		case '&':
			b.WriteString("&amp;")
		case '\'':
			b.WriteString("&#39;")
		case '+':
			b.WriteString("&#43;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// JSEscapeString returns the escaped JavaScript equivalent of the plain text
// data s, exactly as text/template's js function does.
func JSEscapeString(s string) string {
	// Avoid allocation if we can.
	if strings.IndexFunc(s, jsIsSpecial) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !jsIsSpecial(rune(c)) {
			// fast path: nothing to do
			continue
		}
		b.WriteString(s[last:i])
		if c < utf8.RuneSelf {
			// Quotes, slashes and angle brackets get quoted.
			// Control characters get written as \u00XX.
			switch c {
			case '\\':
				b.WriteString(`\\`)
			case '\'':
				b.WriteString(`\'`)
			case '"':
				b.WriteString(`\"`)
			case '<':
				b.WriteString(`\u003C`)
			case '>':
				b.WriteString(`\u003E`)
			case '&':
				b.WriteString(`\u0026`)
			case '=':
				b.WriteString(`\u003D`)
			default:
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[c>>4])
				b.WriteByte(hexDigits[c&0xF])
			}
		} else {
			// Unicode rune.
			r, size := utf8.DecodeRuneInString(s[i:])
			if unicode.IsPrint(r) {
				b.WriteString(s[i : i+size])
			} else {
				fmt.Fprintf(&b, "\\u%04X", r)
			}
			i += size - 1
		}
		last = i + 1
	}
	b.WriteString(s[last:])
	return b.String()
}

const hexDigits = "0123456789ABCDEF"

func jsIsSpecial(r rune) bool {
	switch r {
	case '\\', '\'', '"', '<', '>', '&', '=':
		return true
	}
	return r < ' ' || utf8.RuneSelf <= r
}
