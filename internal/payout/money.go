// Package payout reconciles the lump sums a delivery aggregator settles
// against what the restaurant's own order book says it should have been paid,
// and works out what each dish really earns once commission, packaging and
// withholding are taken out.
//
// Every rule in this package is a pure function. Nothing here touches the
// network, the clock or the filesystem.
package payout

import (
	"fmt"
	"strings"
)

// All money in this package is held as whole paise (or centavos) in an int64,
// so that sums and comparisons are exact and never drift. 100 paise = Rs 1.00.
// Percentages are held as basis points, below, for the same reason.

// BasisPoints is one hundredth of one percent. 100 bp = 1.00%, 2250 bp =
// 22.50%, 10 bp = 0.10%. Commission and tax rates are held this way so that a
// rate can be stated to two decimal places — which aggregator contracts do —
// without any binary floating point entering the money path.
type BasisPoints int64

// Percent converts a percentage written as a decimal number into basis points,
// rounding half away from zero. 22.5 becomes 2250.
func Percent(p float64) BasisPoints {
	if p < 0 {
		return -BasisPoints(-p*100 + 0.5)
	}
	return BasisPoints(p*100 + 0.5)
}

// String renders basis points the way a contract states a rate: "22.50%".
func (b BasisPoints) String() string {
	neg := b < 0
	if neg {
		b = -b
	}
	s := fmt.Sprintf("%d.%02d%%", int64(b)/100, int64(b)%100)
	if neg {
		return "-" + s
	}
	return s
}

// ApplyRate returns rate applied to base, in paise, rounded half away from
// zero. It is the only place in this package where a percentage meets money.
func ApplyRate(base int64, rate BasisPoints) int64 {
	if base == 0 || rate == 0 {
		return 0
	}
	n := base * int64(rate) // paise x basis points
	if n < 0 {
		return (n - 5000) / 10000
	}
	return (n + 5000) / 10000
}

// EffectiveRate returns amount expressed as a rate of base, in basis points,
// rounded half away from zero. A zero or negative base has no meaningful rate,
// so the second result is false and callers must not read the first.
func EffectiveRate(amount, base int64) (BasisPoints, bool) {
	if base <= 0 {
		return 0, false
	}
	n := amount * 10000
	if n < 0 {
		return BasisPoints((n - base/2) / base), true
	}
	return BasisPoints((n + base/2) / base), true
}

// ParseAmount reads a money amount written the way aggregator and accounting
// exports write it, and returns whole paise.
//
// It accepts a leading currency marker (Rs, Rs., INR, the rupee or peso sign,
// PHP, P), Indian or Western digit grouping ("1,00,000.50" and "100,000.50"),
// a sign, accounting negatives in brackets ("(500)" is -500), surrounding and
// embedded spaces, and the dashes and blanks spreadsheets use for nil ("", "-",
// "--"). Anything beyond two decimal places is rounded half away from zero,
// because money here is never finer than a paisa.
func ParseAmount(s string) (int64, error) {
	t := strings.TrimSpace(s)
	t = strings.NewReplacer(
		" ", "", "\u00a0", "", "\u200b", "", "\t", "",
		"₹", "", "₱", "", ",", "",
	).Replace(t)
	// Drop a currency word or letter, in any case: INR, PHP, Rs, Rs., P.
	for _, p := range []string{"INR", "PHP", "RS.", "RS", "P"} {
		if len(t) >= len(p) && strings.EqualFold(t[:len(p)], p) {
			rest := t[len(p):]
			// "P" must not eat the first letter of something that is not a
			// number at all; only strip it if digits or a sign follow.
			if p == "P" && rest != "" && !(rest[0] >= '0' && rest[0] <= '9') &&
				rest[0] != '.' && rest[0] != '-' && rest[0] != '(' {
				break
			}
			t = rest
			break
		}
	}
	neg := false
	if len(t) >= 2 && t[0] == '(' && t[len(t)-1] == ')' {
		neg = true
		t = t[1 : len(t)-1]
	}
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = neg != (t[0] == '-')
		t = t[1:]
	}
	// Blank and dash-only cells mean zero, not a mistake.
	if t == "" || strings.Trim(t, "-–—.") == "" {
		return 0, nil
	}

	intPart, fracPart := t, ""
	if i := strings.IndexByte(t, '.'); i >= 0 {
		intPart, fracPart = t[:i], t[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return 0, fmt.Errorf("amount %q has more than one decimal point", s)
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	var whole int64
	for i := 0; i < len(intPart); i++ {
		c := intPart[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("amount %q is not a number", s)
		}
		if whole > (1<<58)/100 {
			return 0, fmt.Errorf("amount %q is too large", s)
		}
		whole = whole*10 + int64(c-'0')
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, fmt.Errorf("amount %q is not a number", s)
		}
	}
	paise := whole * 100
	if fracPart != "" {
		switch len(fracPart) {
		case 1:
			paise += int64(fracPart[0]-'0') * 10
		default:
			p := int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
			if len(fracPart) > 2 && fracPart[2] >= '5' {
				p++
			}
			paise += p
		}
	}
	if neg {
		paise = -paise
	}
	return paise, nil
}

// ParseCount reads a whole-number quantity, tolerating blanks (which mean 1,
// because an item line without a quantity is one of that item), decimals that
// are whole ("2.00"), and spaces.
func ParseCount(s string) (int, error) {
	t := strings.TrimSpace(s)
	if t == "" || t == "-" {
		return 1, nil
	}
	p, err := ParseAmount(t)
	if err != nil {
		return 0, fmt.Errorf("quantity %q is not a number", s)
	}
	if p%100 != 0 {
		return 0, fmt.Errorf("quantity %q is not a whole number", s)
	}
	n := p / 100
	if n < 0 {
		return 0, fmt.Errorf("quantity %q is negative", s)
	}
	return int(n), nil
}

// Money formats paise the Indian way — groups of two above the last three
// digits — with two decimal places and no currency symbol, so 1234567890
// becomes "1,23,45,678.90". Safe inside a CSV cell and in plain text.
func Money(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	digits := fmt.Sprintf("%d", paise/100)

	var grouped string
	if len(digits) <= 3 {
		grouped = digits
	} else {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		grouped = strings.Join(parts, ",") + "," + tail
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%02d", sign, grouped, paise%100)
}

// Decimal formats paise as a plain machine-readable decimal ("18000.00") for
// CSV columns a spreadsheet or accounting package will read back.
func Decimal(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%02d", sign, paise/100, paise%100)
}
