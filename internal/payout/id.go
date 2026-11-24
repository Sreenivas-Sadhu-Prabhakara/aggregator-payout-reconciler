package payout

import (
	"strings"
	"unicode"
)

// NormaliseOrderID turns an order reference as written in one export into the
// key used to match it against the other export.
//
// The rule, in full, because matching money depends on it:
//
//  1. Leading and trailing whitespace is trimmed.
//  2. Every space-like character anywhere inside the reference is removed:
//     ordinary spaces, tabs, non-breaking spaces (U+00A0) and zero-width
//     spaces (U+200B). Spreadsheets and PDF-to-CSV conversions insert these
//     freely, so they carry no information.
//  3. A '#' is removed wherever it appears. It is a marker meaning "number",
//     not part of the reference.
//  4. What is left is upper-cased, because the two exports disagree on case
//     for the same order far more often than two different orders differ only
//     by case.
//
// Nothing else is touched. In particular hyphens, slashes and underscores are
// kept, because in aggregator references they separate meaningful parts (a
// city or outlet code from a serial). Dropping them would merge genuinely
// different orders: "A-123" and "A1-23" would both collapse to "A123".
//
// So of the three references in the worked example:
//
//	"#A-123" -> "A-123"
//	"a 123"  -> "A123"
//	"A123"   -> "A123"
//
// the second and third collide, and should: one export merely printed a space
// inside the reference. The first does not collide with them, and should not:
// its hyphen is a real character and may well belong to a different order.
func NormaliseOrderID(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '#':
			continue
		case r == '\u200b' || r == '\ufeff':
			continue
		case unicode.IsSpace(r):
			continue
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}
