package payout

import "testing"

func TestDishKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Paneer Butter Masala", "PANEER BUTTER MASALA"},
		{"  paneer butter masala ", "PANEER BUTTER MASALA"},
		{"Paneer  Butter   Masala", "PANEER BUTTER MASALA"},
		{"paneer\tbutter masala", "PANEER BUTTER MASALA"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := DishKey(tc.in); got != tc.want {
				t.Fatalf("DishKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDishMargin(t *testing.T) {
	tests := []struct {
		name       string
		item       Item
		foodCost   int64
		commission BasisPoints
		packaging  int64
		taxRate    BasisPoints
		want       DishEconomics
	}{
		{
			// THE INSIGHT. A Rs 200 biryani costing Rs 140 to make shows a
			// 30% gross margin on the menu card. After 25% commission, a
			// Rs 15 container and 1.1% withholding, the restaurant keeps
			// Rs 132.80 and loses Rs 7.20 on every single order.
			name:       "profitable gross, loss-making net",
			item:       Item{Name: "Hyderabadi Biryani", Qty: 1, UnitPrice: rs(200)},
			foodCost:   rs(140),
			commission: Percent(25),
			packaging:  rs(15),
			taxRate:    GSTTCSSection52Ceiling + TDSSection194O, // 1.10%
			want: DishEconomics{
				Name: "Hyderabadi Biryani", MenuPrice: rs(200), FoodCost: rs(140),
				Commission: rs(50), Packaging: rs(15), TaxWithheld: 220,
				NetRealisation: 13280,
				GrossMargin:    rs(60), GrossMarginPct: 3000,
				NetMargin: -720, NetMarginPct: -360,
				LossMaking: true, FlattersToDeceive: true,
			},
		},
		{
			name:       "genuinely profitable after commission",
			item:       Item{Name: "Masala Dosa", Qty: 1, UnitPrice: rs(120)},
			foodCost:   rs(30),
			commission: Percent(22.5),
			packaging:  rs(8),
			taxRate:    TDSSection194O,
			want: DishEconomics{
				Name: "Masala Dosa", MenuPrice: rs(120), FoodCost: rs(30),
				Commission: rs(27), Packaging: rs(8), TaxWithheld: 12,
				NetRealisation: 8488,
				GrossMargin:    rs(90), GrossMarginPct: 7500,
				NetMargin: 5488, NetMarginPct: 4573,
				LossMaking: false, FlattersToDeceive: false,
			},
		},
		{
			name:       "loss-making on the menu card already",
			item:       Item{Name: "Loss Leader Thali", Qty: 1, UnitPrice: rs(100)},
			foodCost:   rs(120),
			commission: Percent(20),
			packaging:  0,
			taxRate:    0,
			want: DishEconomics{
				Name: "Loss Leader Thali", MenuPrice: rs(100), FoodCost: rs(120),
				Commission: rs(20), NetRealisation: rs(80),
				GrossMargin: -rs(20), GrossMarginPct: -2000,
				NetMargin: -rs(40), NetMarginPct: -4000,
				LossMaking: true,
				// Not "flatters to deceive": the menu card was already honest
				// about this one.
				FlattersToDeceive: false,
			},
		},
		{
			name:       "exactly break-even net is not loss-making",
			item:       Item{Name: "Break Even", Qty: 1, UnitPrice: rs(100)},
			foodCost:   rs(80),
			commission: Percent(20),
			packaging:  0,
			taxRate:    0,
			want: DishEconomics{
				Name: "Break Even", MenuPrice: rs(100), FoodCost: rs(80),
				Commission: rs(20), NetRealisation: rs(80),
				GrossMargin: rs(20), GrossMarginPct: 2000,
				NetMargin: 0, NetMarginPct: 0,
				LossMaking: false, FlattersToDeceive: false,
			},
		},
		{
			name:       "one paisa below break-even flips the verdict",
			item:       Item{Name: "Knife Edge", Qty: 1, UnitPrice: rs(100)},
			foodCost:   rs(80) + 1,
			commission: Percent(20),
			want: DishEconomics{
				Name: "Knife Edge", MenuPrice: rs(100), FoodCost: rs(80) + 1,
				Commission: rs(20), NetRealisation: rs(80),
				GrossMargin: rs(20) - 1, GrossMarginPct: 1999,
				NetMargin: -1, NetMarginPct: -1,
				LossMaking: true, FlattersToDeceive: true,
			},
		},
		{
			name:       "no food cost entered leaves the menu price as the margin",
			item:       Item{Name: "Unknown Cost", Qty: 1, UnitPrice: rs(100)},
			foodCost:   0,
			commission: Percent(20),
			want: DishEconomics{
				Name: "Unknown Cost", MenuPrice: rs(100),
				Commission: rs(20), NetRealisation: rs(80),
				GrossMargin: rs(100), GrossMarginPct: 10000,
				NetMargin: rs(80), NetMarginPct: 8000,
			},
		},
		{
			name:       "a free item has no measurable margin percentage",
			item:       Item{Name: "Complimentary Papad", Qty: 1, UnitPrice: 0},
			foodCost:   rs(5),
			commission: Percent(20),
			want: DishEconomics{
				Name: "Complimentary Papad", FoodCost: rs(5),
				GrossMargin: -rs(5), NetMargin: -rs(5),
				LossMaking: true,
			},
		},
		{
			name:       "the item quantity does not affect a per-unit figure",
			item:       Item{Name: "Bulk Order", Qty: 10, UnitPrice: rs(100)},
			foodCost:   rs(50),
			commission: Percent(20),
			want: DishEconomics{
				Name: "Bulk Order", MenuPrice: rs(100), FoodCost: rs(50),
				Commission: rs(20), NetRealisation: rs(80),
				GrossMargin: rs(50), GrossMarginPct: 5000,
				NetMargin: rs(30), NetMarginPct: 3000,
			},
		},
		{
			name:       "name is trimmed",
			item:       Item{Name: "  Spaced Out  ", Qty: 1, UnitPrice: rs(100)},
			commission: 0,
			want: DishEconomics{
				Name: "Spaced Out", MenuPrice: rs(100), NetRealisation: rs(100),
				GrossMargin: rs(100), GrossMarginPct: 10000,
				NetMargin: rs(100), NetMarginPct: 10000,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DishMargin(tc.item, tc.foodCost, tc.commission, tc.packaging, tc.taxRate)
			if got != tc.want {
				t.Fatalf("DishMargin mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
			// Net realisation must always equal the documented formula.
			want := got.MenuPrice - got.Commission - got.Packaging - got.TaxWithheld
			if got.NetRealisation != want {
				t.Fatalf("NetRealisation %d does not match the formula %d", got.NetRealisation, want)
			}
			if got.NetMargin != got.NetRealisation-got.FoodCost {
				t.Fatal("NetMargin must be net realisation less food cost")
			}
		})
	}
}

// TestDishMarginGrossMisleads states the product's whole argument as an
// assertion: a menu built on gross margin can be entirely loss-making on the
// platform.
func TestDishMarginGrossMisleads(t *testing.T) {
	// A 30% gross margin is a healthy-looking food cost ratio.
	d := DishMargin(
		Item{Name: "Butter Chicken", Qty: 1, UnitPrice: rs(400)},
		rs(280), // 70% food cost ratio
		Percent(24),
		rs(25),
		GSTTCSSection52Ceiling+TDSSection194O,
	)
	if d.GrossMargin <= 0 {
		t.Fatalf("the dish should look profitable on the menu card, gross margin was %d", d.GrossMargin)
	}
	if d.GrossMarginPct != 3000 {
		t.Fatalf("gross margin should read 30.00%%, got %s", d.GrossMarginPct)
	}
	if !d.LossMaking {
		t.Fatalf("the dish should be loss-making after commission; net margin was %s", Money(d.NetMargin))
	}
	if !d.FlattersToDeceive {
		t.Fatal("a dish with a positive gross and negative net margin must be flagged")
	}
	// 400 - 96 commission - 25 packaging - 4.40 withheld = 274.60; less 280 of
	// food cost is a loss of 5.40 on every order.
	if want := int64(-540); d.NetMargin != want {
		t.Fatalf("net margin = %s, want %s", Money(d.NetMargin), Money(want))
	}
}

func TestWorstDishes(t *testing.T) {
	opts := Options{
		AgreedCommissionPct: Percent(25),
		Rates:               RatesIndiaRestaurant(), // TCS nil, TDS 0.10%
	}
	orders := []Order{
		{
			ID: "A1", GrossValue: rs(320), PackagingCharge: rs(20),
			Items: []Item{
				{Name: "Biryani", Qty: 1, UnitPrice: rs(200)},
				{Name: "Dosa", Qty: 1, UnitPrice: rs(120)},
			},
		},
		{
			ID: "A2", GrossValue: rs(400), PackagingCharge: rs(20),
			Items: []Item{{Name: "biryani", Qty: 2, UnitPrice: rs(200)}},
		},
	}
	costs := map[string]int64{
		DishKey("Biryani"): rs(140),
		// Dosa has no cost entered on purpose.
	}
	got := WorstDishes(orders, costs, opts)
	if len(got) != 2 {
		t.Fatalf("got %d dishes, want 2", len(got))
	}

	// Case folding must merge "Biryani" and "biryani" into one dish.
	if got[0].Name != "Biryani" {
		t.Fatalf("worst dish = %q, want Biryani (it loses money on every order)", got[0].Name)
	}
	if got[0].Units != 3 {
		t.Fatalf("Biryani units = %d, want 3 across both orders", got[0].Units)
	}
	if !got[0].CostKnown {
		t.Fatal("Biryani has a food cost entered and must be reported as known")
	}
	if !got[0].LossMaking {
		t.Fatalf("Biryani must be loss-making; net margin %s", Money(got[0].NetMargin))
	}
	if got[0].TotalNetMargin >= 0 {
		t.Fatalf("Biryani total net margin = %s, want a loss", Money(got[0].TotalNetMargin))
	}
	if got[0].TotalGrossMargin <= 0 {
		t.Fatal("Biryani must still show a positive total gross margin — that is the trap")
	}
	if got[0].TotalNetMargin != got[0].NetMargin*int64(got[0].Units) {
		t.Fatal("total net margin must be units times per-unit net margin")
	}

	// Sorting is worst-first by money lost.
	if got[0].TotalNetMargin > got[1].TotalNetMargin {
		t.Fatalf("dishes are not sorted worst-first: %s then %s",
			Money(got[0].TotalNetMargin), Money(got[1].TotalNetMargin))
	}
	if got[1].Name != "Dosa" {
		t.Fatalf("second dish = %q, want Dosa", got[1].Name)
	}
	if got[1].CostKnown {
		t.Fatal("Dosa has no food cost entered and must be reported as unknown")
	}
	if got[1].Revenue != rs(120) {
		t.Fatalf("Dosa revenue = %s, want %s", Money(got[1].Revenue), Money(rs(120)))
	}
}

func TestWorstDishesPackagingSplit(t *testing.T) {
	opts := Options{AgreedCommissionPct: 0, Rates: RatesNone()}
	// Rs 30 of packaging across lines worth Rs 200 and Rs 100: the split is
	// 20 and 10, and it must be exact with nothing lost to rounding.
	orders := []Order{{
		ID: "A1", GrossValue: rs(300), PackagingCharge: rs(30),
		Items: []Item{
			{Name: "Big", Qty: 1, UnitPrice: rs(200)},
			{Name: "Small", Qty: 1, UnitPrice: rs(100)},
		},
	}}
	got := WorstDishes(orders, nil, opts)
	byName := map[string]DishRoll{}
	var total int64
	for _, d := range got {
		byName[d.Name] = d
		total += d.Packaging * int64(d.Units)
	}
	if total != rs(30) {
		t.Fatalf("packaging attributed = %s, want the whole %s", Money(total), Money(rs(30)))
	}
	if byName["Big"].Packaging != rs(20) {
		t.Fatalf("Big packaging = %s, want %s", Money(byName["Big"].Packaging), Money(rs(20)))
	}
	if byName["Small"].Packaging != rs(10) {
		t.Fatalf("Small packaging = %s, want %s", Money(byName["Small"].Packaging), Money(rs(10)))
	}

	// When packaging is not borne by the restaurant, no dish carries any.
	opts.Rates.PackagingBorneByRestaurant = false
	for _, d := range WorstDishes(orders, nil, opts) {
		if d.Packaging != 0 {
			t.Fatalf("%s carries packaging %s although the platform bears it",
				d.Name, Money(d.Packaging))
		}
	}
}

func TestWorstDishesEdgeCases(t *testing.T) {
	opts := Options{AgreedCommissionPct: Percent(20), Rates: RatesNone()}

	if got := WorstDishes(nil, nil, opts); len(got) != 0 {
		t.Fatalf("no orders should produce no dishes, got %d", len(got))
	}
	if got := WorstDishes([]Order{{ID: "A1", GrossValue: rs(100)}}, nil, opts); len(got) != 0 {
		t.Fatalf("an order with no item lines should produce no dishes, got %d", len(got))
	}
	// Blank names and zero quantities are skipped rather than crashing.
	orders := []Order{{
		ID: "A1", GrossValue: rs(100),
		Items: []Item{
			{Name: "   ", Qty: 1, UnitPrice: rs(50)},
			{Name: "Zero Qty", Qty: 0, UnitPrice: rs(50)},
			{Name: "Real", Qty: 1, UnitPrice: rs(50)},
		},
	}}
	got := WorstDishes(orders, nil, opts)
	if len(got) != 1 || got[0].Name != "Real" {
		t.Fatalf("got %+v, want only the one real dish", got)
	}
}

// TestWorstDishesStableOrder guards the tie-break, so that the table the owner
// reads does not reshuffle itself between runs.
func TestWorstDishesStableOrder(t *testing.T) {
	opts := Options{AgreedCommissionPct: Percent(20), Rates: RatesNone()}
	orders := []Order{{
		ID: "A1", GrossValue: rs(300),
		Items: []Item{
			{Name: "Zebra", Qty: 1, UnitPrice: rs(100)},
			{Name: "Apple", Qty: 1, UnitPrice: rs(100)},
			{Name: "Mango", Qty: 1, UnitPrice: rs(100)},
		},
	}}
	first := WorstDishes(orders, nil, opts)
	for i := 0; i < 5; i++ {
		again := WorstDishes(orders, nil, opts)
		for j := range first {
			if first[j].Name != again[j].Name {
				t.Fatalf("run %d reordered the table: %q then %q", i, first[j].Name, again[j].Name)
			}
		}
	}
	// Identical margins must sort by name.
	want := []string{"Apple", "Mango", "Zebra"}
	for i, n := range want {
		if first[i].Name != n {
			t.Fatalf("position %d = %q, want %q", i, first[i].Name, n)
		}
	}
}
