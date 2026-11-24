package payout

import "testing"

// rs turns rupees written as a plain number into paise, so the test tables
// read like a settlement statement.
func rs(r int64) int64 { return r * 100 }

func TestCommissionableBase(t *testing.T) {
	tests := []struct {
		name string
		o    Order
		want int64
	}{
		{
			name: "gross with no discount",
			o:    Order{GrossValue: rs(1000)},
			want: rs(1000),
		},
		{
			name: "restaurant funded discount is removed from the base",
			o:    Order{GrossValue: rs(1000), RestaurantDiscount: rs(100)},
			want: rs(900),
		},
		{
			name: "aggregator funded discount is NOT removed from the base",
			o:    Order{GrossValue: rs(1000), AggregatorDiscount: rs(300)},
			want: rs(1000),
		},
		{
			name: "both discounts: only the restaurant's leaves the base",
			o:    Order{GrossValue: rs(1000), RestaurantDiscount: rs(100), AggregatorDiscount: rs(300)},
			want: rs(900),
		},
		{
			name: "packaging is outside the base",
			o:    Order{GrossValue: rs(1000), PackagingCharge: rs(20)},
			want: rs(1000),
		},
		{
			name: "a discount larger than the order floors the base at zero",
			o:    Order{GrossValue: rs(100), RestaurantDiscount: rs(150)},
			want: 0,
		},
		{
			name: "empty order",
			o:    Order{},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CommissionableBase(tc.o); got != tc.want {
				t.Fatalf("CommissionableBase = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCustomerPaid(t *testing.T) {
	tests := []struct {
		name string
		o    Order
		want int64
	}{
		{
			name: "both discounts come off and packaging goes on",
			o:    Order{GrossValue: rs(1000), RestaurantDiscount: rs(100), AggregatorDiscount: rs(200), PackagingCharge: rs(20)},
			want: rs(720),
		},
		{name: "plain order", o: Order{GrossValue: rs(500)}, want: rs(500)},
		{name: "floored at zero", o: Order{GrossValue: rs(100), AggregatorDiscount: rs(200)}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CustomerPaid(tc.o); got != tc.want {
				t.Fatalf("CustomerPaid = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExpectedPayout(t *testing.T) {
	tests := []struct {
		name  string
		o     Order
		pct   BasisPoints
		rates RateSet
		want  Expected
	}{
		{
			// Rs 1,000 order, 22.5% commission, restaurant-service rates:
			// no TCS, 0.10% TDS, packaging borne by the restaurant.
			name:  "plain order at restaurant service rates",
			o:     Order{ID: "A1", GrossValue: rs(1000), PackagingCharge: rs(20)},
			pct:   Percent(22.5),
			rates: RatesIndiaRestaurant(),
			want: Expected{
				OrderID: "A1", Base: rs(1000), GrossValue: rs(1000),
				CommissionPct: 2250, Commission: rs(225),
				Packaging: rs(20), GSTTCS: 0, TDS194O: rs(1), TaxWithheld: rs(1),
				Payout: rs(754),
			},
		},
		{
			// The case that costs restaurants money: the platform funded a
			// Rs 300 discount. Commission is still due on the full Rs 1,000,
			// and the restaurant is still owed the full amount.
			name:  "aggregator funded discount does not reduce the payout",
			o:     Order{ID: "A2", GrossValue: rs(1000), AggregatorDiscount: rs(300)},
			pct:   Percent(20),
			rates: RatesNone(),
			want: Expected{
				OrderID: "A2", Base: rs(1000), GrossValue: rs(1000), AggregatorDiscount: rs(300),
				CommissionPct: 2000, Commission: rs(200), Payout: rs(800),
			},
		},
		{
			name:  "restaurant funded discount reduces both base and payout",
			o:     Order{ID: "A3", GrossValue: rs(1000), RestaurantDiscount: rs(100)},
			pct:   Percent(20),
			rates: RatesNone(),
			want: Expected{
				OrderID: "A3", Base: rs(900), GrossValue: rs(1000), RestaurantDiscount: rs(100),
				CommissionPct: 2000, Commission: rs(180), Payout: rs(720),
			},
		},
		{
			name:  "packaging not borne by the restaurant is not deducted",
			o:     Order{ID: "A4", GrossValue: rs(1000), PackagingCharge: rs(20)},
			pct:   Percent(20),
			rates: RateSet{Name: "no packaging cost", PackagingBorneByRestaurant: false},
			want: Expected{
				OrderID: "A4", Base: rs(1000), GrossValue: rs(1000),
				CommissionPct: 2000, Commission: rs(200), Packaging: 0, Payout: rs(800),
			},
		},
		{
			// Both India withholding rates, so the two named constants are
			// exercised: 1.00% TCS and 0.10% TDS of a Rs 1,000 base.
			name:  "both India withholding rates apply",
			o:     Order{ID: "A5", GrossValue: rs(1000)},
			pct:   Percent(20),
			rates: RatesIndia(),
			want: Expected{
				OrderID: "A5", Base: rs(1000), GrossValue: rs(1000),
				CommissionPct: 2000, Commission: rs(200),
				GSTTCS: rs(10), TDS194O: rs(1), TaxWithheld: rs(11),
				Payout: rs(789),
			},
		},
		{
			name:  "payment gateway fee is charged on what the customer paid",
			o:     Order{ID: "A6", GrossValue: rs(1000), AggregatorDiscount: rs(200), PackagingCharge: rs(20)},
			pct:   Percent(20),
			rates: RateSet{Name: "pg 2%", PGFee: Percent(2), PackagingBorneByRestaurant: true},
			// Customer paid 1000 - 0 - 200 + 20 = 820; 2% of that is 16.40.
			want: Expected{
				OrderID: "A6", Base: rs(1000), GrossValue: rs(1000), AggregatorDiscount: rs(200),
				CommissionPct: 2000, Commission: rs(200), PGFee: 1640, Packaging: rs(20),
				Payout: rs(1000) - rs(200) - 1640 - rs(20),
			},
		},
		{
			name:  "zero value order",
			o:     Order{ID: "A7"},
			pct:   Percent(22.5),
			rates: RatesIndia(),
			want:  Expected{OrderID: "A7", CommissionPct: 2250},
		},
		{
			name:  "philippines rate set withholds nothing",
			o:     Order{ID: "A8", GrossValue: rs(1000)},
			pct:   Percent(18),
			rates: RatesPhilippines(),
			want: Expected{
				OrderID: "A8", Base: rs(1000), GrossValue: rs(1000),
				CommissionPct: 1800, Commission: rs(180), Payout: rs(820),
			},
		},
		{
			name:  "commission rounds half away from zero",
			o:     Order{ID: "A9", GrossValue: 10050}, // Rs 100.50
			pct:   Percent(22.5),
			rates: RatesNone(),
			// 22.5% of 10050 paise = 2261.25 -> 2261
			want: Expected{
				OrderID: "A9", Base: 10050, GrossValue: 10050,
				CommissionPct: 2250, Commission: 2261, Payout: 10050 - 2261,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpectedPayout(tc.o, tc.pct, tc.rates)
			if got != tc.want {
				t.Fatalf("ExpectedPayout mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
			// The payout must always equal the formula stated in the docs.
			sum := tc.o.GrossValue - tc.o.RestaurantDiscount - got.Commission -
				got.PGFee - got.Packaging - got.TaxWithheld
			if got.Payout != sum {
				t.Fatalf("payout %d does not equal the documented formula %d", got.Payout, sum)
			}
		})
	}
}

func TestExpectedWithPGFee(t *testing.T) {
	o := Order{ID: "A1", GrossValue: rs(1000)}
	rates := RateSet{Name: "pg 2%", PGFee: Percent(2)}
	e := ExpectedPayout(o, Percent(20), rates)
	if e.PGFee != rs(20) {
		t.Fatalf("expected PG fee %d, want %d", e.PGFee, rs(20))
	}
	before := e.Payout
	// The statement charged Rs 25 instead of the expected Rs 20.
	got := e.WithPGFee(rs(25))
	if got.PGFee != rs(25) {
		t.Fatalf("PGFee = %d, want %d", got.PGFee, rs(25))
	}
	if want := before - rs(5); got.Payout != want {
		t.Fatalf("payout = %d, want %d (the extra Rs 5 of fee)", got.Payout, want)
	}
	// Substituting the same fee must change nothing.
	if same := e.WithPGFee(e.PGFee); same != e {
		t.Fatalf("substituting the same fee changed the expectation: %+v vs %+v", same, e)
	}
}

func TestVariance(t *testing.T) {
	const tol = DefaultTolerancePaise // Rs 1.00
	tests := []struct {
		name      string
		net, exp  int64
		tol       int64
		wantDelta int64
		wantClass Class
	}{
		{name: "exact match", net: rs(754), exp: rs(754), tol: tol, wantDelta: 0, wantClass: ClassOK},
		{name: "one paisa short is within tolerance", net: rs(754) - 1, exp: rs(754), tol: tol, wantDelta: -1, wantClass: ClassOK},
		{name: "short by exactly the tolerance is OK", net: rs(753), exp: rs(754), tol: tol, wantDelta: -100, wantClass: ClassOK},
		{name: "short by one paisa more than the tolerance", net: rs(753) - 1, exp: rs(754), tol: tol, wantDelta: -101, wantClass: ClassShortPaid},
		{name: "over by exactly the tolerance is OK", net: rs(755), exp: rs(754), tol: tol, wantDelta: 100, wantClass: ClassOK},
		{name: "over by one paisa more than the tolerance", net: rs(755) + 1, exp: rs(754), tol: tol, wantDelta: 101, wantClass: ClassOverPaid},
		{name: "badly short", net: rs(600), exp: rs(754), tol: tol, wantDelta: -rs(154), wantClass: ClassShortPaid},
		{name: "zero tolerance catches a single paisa", net: rs(754) - 1, exp: rs(754), tol: 0, wantDelta: -1, wantClass: ClassShortPaid},
		{name: "a negative tolerance is read as its magnitude", net: rs(753), exp: rs(754), tol: -100, wantDelta: -100, wantClass: ClassOK},
		{name: "nothing settled at all", net: 0, exp: rs(754), tol: tol, wantDelta: -rs(754), wantClass: ClassShortPaid},
		{name: "nothing expected and nothing settled", net: 0, exp: 0, tol: tol, wantDelta: 0, wantClass: ClassOK},
		{name: "a recovery against a zero expectation", net: -rs(50), exp: 0, tol: tol, wantDelta: -rs(50), wantClass: ClassShortPaid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			delta, cls := Variance(tc.net, tc.exp, tc.tol)
			if delta != tc.wantDelta {
				t.Fatalf("Variance delta = %d, want %d", delta, tc.wantDelta)
			}
			if cls != tc.wantClass {
				t.Fatalf("Variance class = %q, want %q", cls, tc.wantClass)
			}
		})
	}
}

func TestCommissionOvercharge(t *testing.T) {
	agreed := Percent(22.5)
	base := Order{ID: "A1", GrossValue: rs(1000)}

	tests := []struct {
		name        string
		o           Order
		charged     int64
		agreed      BasisPoints
		tol         BasisPoints
		wantFlagged bool
		wantEff     BasisPoints
		wantExcess  int64
		wantMeasure bool
	}{
		{
			// The boundary the brief asks for: charged at exactly the agreed
			// rate must never be flagged.
			name: "exactly at the agreed rate is not an overcharge",
			o:    base, charged: rs(225), agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: false, wantEff: 2250, wantExcess: 0, wantMeasure: true,
		},
		{
			name: "one paisa under the agreed rate",
			o:    base, charged: rs(225) - 1, agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: false, wantEff: 2250, wantExcess: -1, wantMeasure: true,
		},
		{
			// 22501 paise of 100000 is 2250.1 bp, which rounds to 2250 and so
			// sits inside the rounding tolerance.
			name: "one paisa over is absorbed by the rounding tolerance",
			o:    base, charged: rs(225) + 1, agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: false, wantEff: 2250, wantExcess: 1, wantMeasure: true,
		},
		{
			name: "2.5 points over the agreed rate is flagged",
			o:    base, charged: rs(250), agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: true, wantEff: 2500, wantExcess: rs(25), wantMeasure: true,
		},
		{
			name: "a tenth of a point over is flagged at the default tolerance",
			o:    base, charged: rs(226), agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: true, wantEff: 2260, wantExcess: rs(1), wantMeasure: true,
		},
		{
			name: "a wider tolerance absorbs a tenth of a point",
			o:    base, charged: rs(226), agreed: agreed, tol: Percent(0.5),
			wantFlagged: false, wantEff: 2260, wantExcess: rs(1), wantMeasure: true,
		},
		{
			// Commission charged on the pre-discount value while the base is
			// post-discount: the classic overcharge.
			name:    "commission charged on the pre-discount value",
			o:       Order{ID: "A2", GrossValue: rs(1000), RestaurantDiscount: rs(200)},
			charged: rs(225), agreed: agreed, tol: DefaultCommissionTolerance,
			// Base is 800; 225 of 800 is 28.125% -> 2813 bp (rounded).
			wantFlagged: true, wantEff: 2813, wantExcess: rs(225) - rs(180), wantMeasure: true,
		},
		{
			name:    "a fully discounted order has no measurable rate",
			o:       Order{ID: "A3", GrossValue: rs(100), RestaurantDiscount: rs(100)},
			charged: rs(5), agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: false, wantExcess: rs(5), wantMeasure: false,
		},
		{
			name: "no commission charged at all",
			o:    base, charged: 0, agreed: agreed, tol: DefaultCommissionTolerance,
			wantFlagged: false, wantEff: 0, wantExcess: -rs(225), wantMeasure: true,
		},
		{
			name: "a negative tolerance is read as its magnitude",
			o:    base, charged: rs(226), agreed: agreed, tol: -Percent(0.5),
			wantFlagged: false, wantEff: 2260, wantExcess: rs(1), wantMeasure: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CommissionOvercharge(tc.o, tc.charged, tc.agreed, tc.tol)
			if got.Flagged != tc.wantFlagged {
				t.Fatalf("Flagged = %v, want %v (effective %s, agreed %s)",
					got.Flagged, tc.wantFlagged, got.EffectivePct, tc.agreed)
			}
			if got.Measurable != tc.wantMeasure {
				t.Fatalf("Measurable = %v, want %v", got.Measurable, tc.wantMeasure)
			}
			if got.Measurable && got.EffectivePct != tc.wantEff {
				t.Fatalf("EffectivePct = %d bp, want %d", got.EffectivePct, tc.wantEff)
			}
			if got.Excess != tc.wantExcess {
				t.Fatalf("Excess = %d paise, want %d", got.Excess, tc.wantExcess)
			}
			if got.Flagged && got.Reason() == "" {
				t.Fatal("a flagged overcharge must carry a reason for the dispute list")
			}
			if !got.Flagged && got.Reason() != "" {
				t.Fatalf("an unflagged overcharge must not carry a reason, got %q", got.Reason())
			}
		})
	}
}

// TestCommissionOverchargeExcessIsTheShortfall ties the two rules together:
// because the expected payout uses the agreed rate, the excess commission on
// an order is exactly the amount by which that order is short-paid. This is
// why the totals must not add the two together.
func TestCommissionOverchargeExcessIsTheShortfall(t *testing.T) {
	o := Order{ID: "A1", GrossValue: rs(1000)}
	agreed := Percent(22.5)
	rates := RatesIndiaRestaurant()
	exp := ExpectedPayout(o, agreed, rates)

	charged := rs(250) // 25% instead of 22.5%
	oc := CommissionOvercharge(o, charged, agreed, DefaultCommissionTolerance)
	if !oc.Flagged {
		t.Fatal("25% against an agreed 22.5% must be flagged")
	}
	// The platform settled correctly on every other line, but used 25%.
	net := exp.Payout - oc.Excess
	delta, cls := Variance(net, exp.Payout, DefaultTolerancePaise)
	if cls != ClassShortPaid {
		t.Fatalf("class = %q, want %q", cls, ClassShortPaid)
	}
	if -delta != oc.Excess {
		t.Fatalf("shortfall %d does not equal the excess commission %d", -delta, oc.Excess)
	}
}

func TestStatutoryConstants(t *testing.T) {
	tests := []struct {
		name string
		got  BasisPoints
		want BasisPoints
		pct  string
	}{
		{"GST TCS section 52 ceiling", GSTTCSSection52Ceiling, 100, "1.00%"},
		{"GST TCS section 52 notified from 10 July 2024", GSTTCSSection52Notified, 50, "0.50%"},
		{"TDS section 194-O from 1 October 2024", TDSSection194O, 10, "0.10%"},
		{"TDS section 194-O until 30 September 2024", TDSSection194OUntilSep2024, 100, "1.00%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("%s = %d bp, want %d", tc.name, tc.got, tc.want)
			}
			if tc.got.String() != tc.pct {
				t.Fatalf("%s renders as %q, want %q", tc.name, tc.got.String(), tc.pct)
			}
		})
	}
}

func TestPresets(t *testing.T) {
	tests := []struct {
		name    string
		rates   RateSet
		wantTCS BasisPoints
		wantTDS BasisPoints
	}{
		{"india default", RatesIndia(), GSTTCSSection52Ceiling, TDSSection194O},
		{"india restaurant service s.9(5)", RatesIndiaRestaurant(), 0, TDSSection194O},
		{"philippines asserts no rate", RatesPhilippines(), 0, 0},
		{"none", RatesNone(), 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rates.GSTTCS != tc.wantTCS {
				t.Fatalf("GSTTCS = %d bp, want %d", tc.rates.GSTTCS, tc.wantTCS)
			}
			if tc.rates.TDS194O != tc.wantTDS {
				t.Fatalf("TDS194O = %d bp, want %d", tc.rates.TDS194O, tc.wantTDS)
			}
			if tc.rates.Name == "" {
				t.Fatal("every rate set must name itself, because exports quote it")
			}
		})
	}
	if len(Presets()) != 4 {
		t.Fatalf("Presets() returned %d rate sets, want 4", len(Presets()))
	}
}

func TestOptionsDefaults(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		wantTol  int64
		wantCTol BasisPoints
	}{
		{"zero means the documented default", Options{}, DefaultTolerancePaise, DefaultCommissionTolerance},
		{"negative means the documented default", Options{TolerancePaise: -5, CommissionTolerance: -5}, DefaultTolerancePaise, DefaultCommissionTolerance},
		{"an explicit value is honoured", Options{TolerancePaise: 500, CommissionTolerance: 50}, 500, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.opts.Tolerance(); got != tc.wantTol {
				t.Fatalf("Tolerance() = %d, want %d", got, tc.wantTol)
			}
			if got := tc.opts.CommissionTol(); got != tc.wantCTol {
				t.Fatalf("CommissionTol() = %d, want %d", got, tc.wantCTol)
			}
		})
	}
}
