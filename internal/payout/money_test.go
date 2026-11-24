package payout

import "testing"

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "plain rupees", in: "500", want: 50000},
		{name: "two decimals", in: "500.25", want: 50025},
		{name: "one decimal pads", in: "500.2", want: 50020},
		{name: "three decimals round up", in: "0.125", want: 13},
		{name: "three decimals round down", in: "0.124", want: 12},
		{name: "half rounds away from zero", in: "0.005", want: 1},
		{name: "negative half rounds away from zero", in: "-0.005", want: -1},
		{name: "indian grouping", in: "1,00,000.50", want: 10000050},
		{name: "western grouping", in: "100,000.50", want: 10000050},
		{name: "rupee sign", in: "\u20b9 1,250.00", want: 125000},
		{name: "peso sign", in: "\u20b1 1,250.00", want: 125000},
		{name: "rs prefix", in: "Rs. 99", want: 9900},
		{name: "inr prefix lowercase", in: "inr99", want: 9900},
		{name: "php prefix", in: "PHP 99", want: 9900},
		{name: "accounting negative", in: "(500.00)", want: -50000},
		{name: "explicit negative", in: "-500", want: -50000},
		{name: "double negative in brackets", in: "(-500)", want: 50000},
		{name: "empty is zero", in: "", want: 0},
		{name: "dash is zero", in: "-", want: 0},
		{name: "double dash is zero", in: "--", want: 0},
		{name: "en dash is zero", in: "\u2013", want: 0},
		{name: "spaces only is zero", in: "   ", want: 0},
		{name: "non breaking space inside", in: "1\u00a0234.00", want: 123400},
		{name: "leading dot", in: ".75", want: 75},
		{name: "zero", in: "0", want: 0},
		{name: "letters are an error", in: "abc", wantErr: true},
		{name: "two decimal points are an error", in: "1.2.3", wantErr: true},
		{name: "trailing letters are an error", in: "100x", wantErr: true},
		{name: "absurdly large is an error", in: "999999999999999999999", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAmount(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAmount(%q) = %d, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAmount(%q) returned %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseAmount(%q) = %d paise, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseCount(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "1", want: 1},
		{in: "3", want: 3},
		{in: "", want: 1},  // an item line with no quantity is one of that item
		{in: "-", want: 1}, // ditto for a dash
		{in: " 2 ", want: 2},
		{in: "2.00", want: 2},
		{in: "0", want: 0},
		{in: "2.5", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "two", wantErr: true},
	}
	for _, tc := range tests {
		t.Run("qty "+tc.in, func(t *testing.T) {
			got, err := ParseCount(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCount(%q) = %d, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCount(%q) returned %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseCount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestPercentAndString(t *testing.T) {
	tests := []struct {
		in   float64
		bp   BasisPoints
		text string
	}{
		{in: 0, bp: 0, text: "0.00%"},
		{in: 0.1, bp: 10, text: "0.10%"},
		{in: 1, bp: 100, text: "1.00%"},
		{in: 0.5, bp: 50, text: "0.50%"},
		{in: 18, bp: 1800, text: "18.00%"},
		{in: 22.5, bp: 2250, text: "22.50%"},
		{in: 23.33, bp: 2333, text: "23.33%"},
		{in: 100, bp: 10000, text: "100.00%"},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			if got := Percent(tc.in); got != tc.bp {
				t.Fatalf("Percent(%v) = %d bp, want %d", tc.in, got, tc.bp)
			}
			if got := tc.bp.String(); got != tc.text {
				t.Fatalf("BasisPoints(%d).String() = %q, want %q", tc.bp, got, tc.text)
			}
		})
	}
}

func TestApplyRate(t *testing.T) {
	tests := []struct {
		name string
		base int64
		rate BasisPoints
		want int64
	}{
		{name: "zero base", base: 0, rate: 2250, want: 0},
		{name: "zero rate", base: 100000, rate: 0, want: 0},
		{name: "22.5% of 1000.00", base: 100000, rate: 2250, want: 22500},
		{name: "1% TCS of 1000.00", base: 100000, rate: GSTTCSSection52Ceiling, want: 1000},
		{name: "0.1% TDS of 1000.00", base: 100000, rate: TDSSection194O, want: 100},
		{name: "0.1% TDS of 455.00 rounds up", base: 45500, rate: TDSSection194O, want: 46},
		{name: "0.1% TDS of 454.00 rounds down", base: 45400, rate: TDSSection194O, want: 45},
		{name: "exact half rounds away from zero", base: 50, rate: 10000, want: 50},
		{name: "exactly half a paisa rounds up", base: 5000, rate: 1, want: 1},
		{name: "just under half a paisa rounds down", base: 4900, rate: 1, want: 0},
		{name: "negative base rounds away from zero", base: -45500, rate: TDSSection194O, want: -46},
		{name: "one paisa base", base: 1, rate: 2250, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyRate(tc.base, tc.rate); got != tc.want {
				t.Fatalf("ApplyRate(%d, %d) = %d, want %d", tc.base, tc.rate, got, tc.want)
			}
		})
	}
}

func TestEffectiveRate(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		base   int64
		want   BasisPoints
		wantOK bool
	}{
		{name: "22.5%", amount: 22500, base: 100000, want: 2250, wantOK: true},
		{name: "25% when 22.5% agreed", amount: 25000, base: 100000, want: 2500, wantOK: true},
		{name: "rounds to nearest bp", amount: 2251, base: 10000, want: 2251, wantOK: true},
		{name: "zero amount", amount: 0, base: 100000, want: 0, wantOK: true},
		{name: "zero base is unmeasurable", amount: 5000, base: 0, wantOK: false},
		{name: "negative base is unmeasurable", amount: 5000, base: -100, wantOK: false},
		{name: "negative amount", amount: -2250, base: 10000, want: -2250, wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EffectiveRate(tc.amount, tc.base)
			if ok != tc.wantOK {
				t.Fatalf("EffectiveRate(%d, %d) ok = %v, want %v", tc.amount, tc.base, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("EffectiveRate(%d, %d) = %d bp, want %d", tc.amount, tc.base, got, tc.want)
			}
		})
	}
}

func TestMoneyAndDecimal(t *testing.T) {
	tests := []struct {
		paise   int64
		money   string
		decimal string
	}{
		{paise: 0, money: "0.00", decimal: "0.00"},
		{paise: 5, money: "0.05", decimal: "0.05"},
		{paise: 100, money: "1.00", decimal: "1.00"},
		{paise: 123456, money: "1,234.56", decimal: "1234.56"},
		{paise: 10000000, money: "1,00,000.00", decimal: "100000.00"},
		{paise: 1234567890, money: "1,23,45,678.90", decimal: "12345678.90"},
		{paise: -45678, money: "-456.78", decimal: "-456.78"},
		{paise: 99900, money: "999.00", decimal: "999.00"},
		{paise: 100000, money: "1,000.00", decimal: "1000.00"},
	}
	for _, tc := range tests {
		t.Run(tc.decimal, func(t *testing.T) {
			if got := Money(tc.paise); got != tc.money {
				t.Fatalf("Money(%d) = %q, want %q", tc.paise, got, tc.money)
			}
			if got := Decimal(tc.paise); got != tc.decimal {
				t.Fatalf("Decimal(%d) = %q, want %q", tc.paise, got, tc.decimal)
			}
		})
	}
}

func TestApplyRateOf(t *testing.T) {
	tests := []struct {
		name                string
		amount, part, whole int64
		want                int64
	}{
		{name: "half", amount: 1000, part: 500, whole: 1000, want: 500},
		{name: "zero whole", amount: 1000, part: 500, whole: 0, want: 0},
		{name: "zero amount", amount: 0, part: 500, whole: 1000, want: 0},
		{name: "zero part", amount: 1000, part: 0, whole: 1000, want: 0},
		{name: "third rounds", amount: 1000, part: 1, whole: 3, want: 333},
		{name: "two thirds rounds up", amount: 1000, part: 2, whole: 3, want: 667},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyRateOf(tc.amount, tc.part, tc.whole); got != tc.want {
				t.Fatalf("ApplyRateOf(%d, %d, %d) = %d, want %d",
					tc.amount, tc.part, tc.whole, got, tc.want)
			}
		})
	}
}
