package payout

import "testing"

func TestNormaliseOrderID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "A123", "A123"},
		{"lower case is raised", "a123", "A123"},
		{"hash marker dropped", "#A123", "A123"},
		{"hash anywhere dropped", "A#123", "A123"},
		{"inner space dropped", "a 123", "A123"},
		{"surrounding space trimmed", "   A123  ", "A123"},
		{"tab dropped", "A\t123", "A123"},
		{"non-breaking space dropped", "A\u00a0123", "A123"},
		{"zero width space dropped", "A\u200b123", "A123"},
		{"byte order mark dropped", "\ufeffA123", "A123"},
		{"hyphen kept", "#A-123", "A-123"},
		{"slash kept", "blr/0012", "BLR/0012"},
		{"underscore kept", "a_1", "A_1"},
		{"empty stays empty", "", ""},
		{"only noise becomes empty", "  # \u00a0 ", ""},
		{"long reference", " #swiggy-2026-11-24/0007 ", "SWIGGY-2026-11-24/0007"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormaliseOrderID(tc.in); got != tc.want {
				t.Fatalf("NormaliseOrderID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormaliseOrderIDCollisions is the rule stated in the doc comment,
// asserted: references collide only when the difference between them is pure
// formatting noise. A hyphen is not noise.
func TestNormaliseOrderIDCollisions(t *testing.T) {
	tests := []struct {
		name        string
		a, b        string
		wantCollide bool
		why         string
	}{
		{
			name: "space and case only", a: "a 123", b: "A123", wantCollide: true,
			why: "the two exports differ only by a space and letter case, which carry no information",
		},
		{
			name: "hash marker only", a: "#A123", b: "A123", wantCollide: true,
			why: "# is a marker meaning number, not part of the reference",
		},
		{
			name: "hyphen is significant", a: "#A-123", b: "A123", wantCollide: false,
			why: "dropping the hyphen would merge genuinely different orders",
		},
		{
			name: "hyphen position is significant", a: "A-123", b: "A1-23", wantCollide: false,
			why: "both would collapse to A123 if hyphens were stripped",
		},
		{
			name: "hash plus space plus case", a: "# a-123 ", b: "A-123", wantCollide: true,
			why: "all three differences are formatting",
		},
		{
			name: "different serials never collide", a: "A124", b: "A123", wantCollide: false,
			why: "different orders",
		},
		{
			name: "outlet prefix is significant", a: "BLR/A123", b: "A123", wantCollide: false,
			why: "the slash-separated outlet code is part of the reference",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ka, kb := NormaliseOrderID(tc.a), NormaliseOrderID(tc.b)
			if got := ka == kb; got != tc.wantCollide {
				t.Fatalf("%q -> %q and %q -> %q: collide = %v, want %v (%s)",
					tc.a, ka, tc.b, kb, got, tc.wantCollide, tc.why)
			}
		})
	}
}
