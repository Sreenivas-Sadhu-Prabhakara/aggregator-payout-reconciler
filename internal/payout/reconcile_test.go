package payout

import (
	"strings"
	"testing"
)

// testOpts are the terms used across the reconciliation tests: 22.5%
// commission, restaurant-service withholding (no TCS, 0.10% TDS), packaging
// borne by the restaurant, Rs 1 tolerance, PG fee accepted as charged.
func testOpts() Options {
	return Options{
		Aggregator:          "Test Platform",
		AgreedCommissionPct: Percent(22.5),
		Rates:               RatesIndiaRestaurant(),
	}
}

func TestReconcileClassifies(t *testing.T) {
	// A Rs 1,000 order: commission 225.00, TDS 1.00, packaging 20.00,
	// so the expected payout is 754.00.
	order := Order{ID: "A1", Date: "2026-11-01", GrossValue: rs(1000), PackagingCharge: rs(20)}
	opts := testOpts()
	expected := ExpectedPayout(order, opts.AgreedCommissionPct, opts.Rates).Payout
	if expected != rs(754) {
		t.Fatalf("test fixture drifted: expected payout %s, want 754.00", Money(expected))
	}

	tests := []struct {
		name      string
		net       int64
		wantClass Class
		wantVar   int64
		wantOwed  int64
	}{
		{name: "paid exactly", net: rs(754), wantClass: ClassOK, wantVar: 0, wantOwed: 0},
		{name: "within tolerance", net: rs(754) - 50, wantClass: ClassOK, wantVar: -50, wantOwed: 0},
		{name: "short paid", net: rs(700), wantClass: ClassShortPaid, wantVar: -rs(54), wantOwed: rs(54)},
		{name: "over paid", net: rs(800), wantClass: ClassOverPaid, wantVar: rs(46), wantOwed: 0},
		{name: "nothing settled on the line", net: 0, wantClass: ClassShortPaid, wantVar: -rs(754), wantOwed: rs(754)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Reconcile(
				[]Order{order},
				[]Settlement{{OrderID: "A1", CommissionCharged: rs(225), TaxWithheld: rs(1), NetSettled: tc.net}},
				nil, opts,
			)
			if len(rep.Rows) != 1 {
				t.Fatalf("got %d rows, want 1", len(rep.Rows))
			}
			row := rep.Rows[0]
			if row.Class != tc.wantClass {
				t.Fatalf("class = %q, want %q (variance %s)", row.Class, tc.wantClass, Money(row.Variance))
			}
			if row.Variance != tc.wantVar {
				t.Fatalf("variance = %s, want %s", Money(row.Variance), Money(tc.wantVar))
			}
			if rep.Totals.MoneyOwedBack != tc.wantOwed {
				t.Fatalf("money owed back = %s, want %s", Money(rep.Totals.MoneyOwedBack), Money(tc.wantOwed))
			}
			if row.Reason == "" {
				t.Fatal("every row must carry a reason")
			}
			if rep.Totals.Matched != 1 || rep.Totals.Orders != 1 || rep.Totals.Settlements != 1 {
				t.Fatalf("counts wrong: %+v", rep.Totals)
			}
		})
	}
}

func TestReconcileMissingAndExtra(t *testing.T) {
	opts := testOpts()
	orders := []Order{
		{ID: "A1", GrossValue: rs(1000)},
		{ID: "A2", GrossValue: rs(500)}, // never settled
	}
	settlements := []Settlement{
		{OrderID: "a 1", CommissionCharged: rs(225), TaxWithheld: rs(1), NetSettled: rs(774)},
		{OrderID: "#X9", NetSettled: rs(300)}, // not in the books
	}
	rep := Reconcile(orders, settlements, nil, opts)

	if len(rep.Rows) != 1 || rep.Rows[0].OrderID != "A1" {
		t.Fatalf("expected A1 to match through normalisation, got %+v", rep.Rows)
	}
	if rep.Totals.InBooksNotSettledCount != 1 {
		t.Fatalf("in books not settled = %d, want 1", rep.Totals.InBooksNotSettledCount)
	}
	if rep.InBooksNotSettled[0].Order.ID != "A2" {
		t.Fatalf("unsettled order = %q, want A2", rep.InBooksNotSettled[0].Order.ID)
	}
	// A2: base 500, commission 112.50, TDS 0.50 -> expected 387.00.
	wantMissing := ExpectedPayout(orders[1], opts.AgreedCommissionPct, opts.Rates).Payout
	if rep.Totals.InBooksNotSettledValue != wantMissing {
		t.Fatalf("unsettled value = %s, want %s", Money(rep.Totals.InBooksNotSettledValue), Money(wantMissing))
	}
	if rep.Totals.SettledNotInBooksCount != 1 {
		t.Fatalf("settled not in books = %d, want 1", rep.Totals.SettledNotInBooksCount)
	}
	if rep.SettledNotInBooks[0].Settlement.OrderID != "X9" {
		t.Fatalf("extra settlement = %q, want X9", rep.SettledNotInBooks[0].Settlement.OrderID)
	}
	if rep.Totals.SettledNotInBooksValue != rs(300) {
		t.Fatalf("extra value = %s, want 300.00", Money(rep.Totals.SettledNotInBooksValue))
	}
	// An extra settlement is money received, not money owed, so it must not
	// enter the headline figure.
	if rep.Totals.MoneyOwedBack != wantMissing {
		t.Fatalf("money owed back = %s, want only the unsettled order %s",
			Money(rep.Totals.MoneyOwedBack), Money(wantMissing))
	}
}

// TestReconcileDoesNotDoubleCountOvercharge is the integrity test on the
// headline number. An overcharged order is short-paid by exactly the excess
// commission; claiming both would overstate the claim.
func TestReconcileDoesNotDoubleCountOvercharge(t *testing.T) {
	opts := testOpts()
	order := Order{ID: "A1", GrossValue: rs(1000)}
	exp := ExpectedPayout(order, opts.AgreedCommissionPct, opts.Rates)
	excess := rs(25) // charged at 25% instead of 22.5%
	rep := Reconcile(
		[]Order{order},
		[]Settlement{{OrderID: "A1", CommissionCharged: rs(250), TaxWithheld: rs(1), NetSettled: exp.Payout - excess}},
		nil, opts,
	)
	if rep.Totals.OverchargeCount != 1 {
		t.Fatalf("overcharge count = %d, want 1", rep.Totals.OverchargeCount)
	}
	if rep.Totals.TotalCommissionOvercharge != excess {
		t.Fatalf("overcharge total = %s, want %s", Money(rep.Totals.TotalCommissionOvercharge), Money(excess))
	}
	if rep.Totals.TotalShortPaid != excess {
		t.Fatalf("short paid = %s, want %s", Money(rep.Totals.TotalShortPaid), Money(excess))
	}
	if rep.Totals.MoneyOwedBack != excess {
		t.Fatalf("money owed back = %s, want %s — the excess commission must not be claimed twice",
			Money(rep.Totals.MoneyOwedBack), Money(excess))
	}
	if !strings.Contains(rep.Rows[0].Reason, "Commission charged") {
		t.Fatalf("reason should explain the overcharge, got %q", rep.Rows[0].Reason)
	}
}

func TestReconcileSumsMultipleSettlementLines(t *testing.T) {
	opts := testOpts()
	order := Order{ID: "A1", GrossValue: rs(1000)}
	exp := ExpectedPayout(order, opts.AgreedCommissionPct, opts.Rates)
	// The platform settled the order in two tranches.
	rep := Reconcile(
		[]Order{order},
		[]Settlement{
			{OrderID: "A1", CommissionCharged: rs(100), TaxWithheld: 50, NetSettled: exp.Payout / 2},
			{OrderID: "A1", CommissionCharged: rs(125), TaxWithheld: 50, NetSettled: exp.Payout - exp.Payout/2},
		},
		nil, opts,
	)
	if len(rep.Rows) != 1 {
		t.Fatalf("got %d rows, want the two tranches folded into 1", len(rep.Rows))
	}
	row := rep.Rows[0]
	if row.Settlement.Lines != 2 {
		t.Fatalf("settlement lines = %d, want 2", row.Settlement.Lines)
	}
	if row.Settlement.NetSettled != exp.Payout {
		t.Fatalf("summed net = %s, want %s", Money(row.Settlement.NetSettled), Money(exp.Payout))
	}
	if row.Settlement.CommissionCharged != rs(225) {
		t.Fatalf("summed commission = %s, want 225.00", Money(row.Settlement.CommissionCharged))
	}
	if row.Class != ClassOK {
		t.Fatalf("class = %q, want OK once the tranches are summed", row.Class)
	}
	if rep.Totals.MultiLineOrders != 1 {
		t.Fatalf("multi-line orders = %d, want 1", rep.Totals.MultiLineOrders)
	}
}

func TestReconcileDuplicateOrderInBooks(t *testing.T) {
	opts := testOpts()
	orders := []Order{
		{ID: "A1", GrossValue: rs(1000), Line: 2},
		{ID: "a1", GrossValue: rs(500), Line: 3}, // same reference, different order
	}
	rep := Reconcile(orders, []Settlement{{OrderID: "A1", NetSettled: rs(754)}}, nil, opts)
	if rep.Totals.Orders != 1 {
		t.Fatalf("orders reconciled = %d, want 1 (the duplicate is reported, not merged)", rep.Totals.Orders)
	}
	if rep.Totals.DuplicateOrderIDs != 1 {
		t.Fatalf("duplicate references = %d, want 1", rep.Totals.DuplicateOrderIDs)
	}
	if len(rep.Problems) != 1 {
		t.Fatalf("got %d problems, want 1", len(rep.Problems))
	}
	if rep.Problems[0].Line != 3 {
		t.Fatalf("problem reported on line %d, want 3", rep.Problems[0].Line)
	}
}

func TestReconcileBlankReferencesAreReported(t *testing.T) {
	opts := testOpts()
	rep := Reconcile(
		[]Order{{ID: "  # ", GrossValue: rs(100), Line: 5}},
		[]Settlement{{OrderID: "", NetSettled: rs(50), Line: 7}},
		nil, opts,
	)
	if len(rep.Rows) != 0 {
		t.Fatalf("got %d rows, want none", len(rep.Rows))
	}
	if len(rep.Problems) != 2 {
		t.Fatalf("got %d problems, want 2 (one per unusable row)", len(rep.Problems))
	}
	sources := map[string]bool{}
	for _, p := range rep.Problems {
		sources[p.Source] = true
	}
	if !sources["orders"] || !sources["settlements"] {
		t.Fatalf("both files should report a problem, got %+v", rep.Problems)
	}
}

func TestReconcileAuditPGFee(t *testing.T) {
	order := Order{ID: "A1", GrossValue: rs(1000)}
	// Agreed PG fee 2% of what the customer paid = Rs 20; the platform
	// charged Rs 30.
	rates := RateSet{Name: "pg 2%", PGFee: Percent(2)}
	sett := []Settlement{{OrderID: "A1", CommissionCharged: rs(200), PGFee: rs(30), NetSettled: rs(770)}}

	tests := []struct {
		name      string
		audit     bool
		wantClass Class
		wantVar   int64
	}{
		{
			name: "not auditing accepts the fee as charged", audit: false,
			// Expected payout with the charged fee: 1000 - 200 - 30 = 770.
			wantClass: ClassOK, wantVar: 0,
		},
		{
			name: "auditing flags the extra Rs 10 of fee", audit: true,
			// Expected payout at the agreed fee: 1000 - 200 - 20 = 780.
			wantClass: ClassShortPaid, wantVar: -rs(10),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{AgreedCommissionPct: Percent(20), Rates: rates, AuditPGFee: tc.audit}
			rep := Reconcile([]Order{order}, sett, nil, opts)
			row := rep.Rows[0]
			if row.Class != tc.wantClass {
				t.Fatalf("class = %q, want %q", row.Class, tc.wantClass)
			}
			if row.Variance != tc.wantVar {
				t.Fatalf("variance = %s, want %s", Money(row.Variance), Money(tc.wantVar))
			}
		})
	}
}

func TestReconcileTotalsAcrossAPeriod(t *testing.T) {
	opts := testOpts()
	orders := []Order{
		{ID: "A1", GrossValue: rs(1000)}, // paid right
		{ID: "A2", GrossValue: rs(1000)}, // short by 50
		{ID: "A3", GrossValue: rs(1000)}, // over by 30
		{ID: "A4", GrossValue: rs(1000)}, // never settled
	}
	e := func(o Order) int64 { return ExpectedPayout(o, opts.AgreedCommissionPct, opts.Rates).Payout }
	settlements := []Settlement{
		{OrderID: "A1", CommissionCharged: rs(225), NetSettled: e(orders[0])},
		{OrderID: "A2", CommissionCharged: rs(225), NetSettled: e(orders[1]) - rs(50)},
		{OrderID: "A3", CommissionCharged: rs(225), NetSettled: e(orders[2]) + rs(30)},
		{OrderID: "A9", NetSettled: rs(10)},
	}
	rep := Reconcile(orders, settlements, nil, opts)

	want := Totals{
		Orders: 4, Settlements: 4, Matched: 3,
		OKCount: 1, ShortPaidCount: 1, OverPaidCount: 1,
		TotalShortPaid: rs(50), TotalOverPaid: rs(30), NetVariance: -rs(20),
		InBooksNotSettledCount: 1, InBooksNotSettledValue: e(orders[3]),
		SettledNotInBooksCount: 1, SettledNotInBooksValue: rs(10),
		MoneyOwedBack: rs(50) + e(orders[3]),
	}
	if rep.Totals != want {
		t.Fatalf("totals mismatch\n got: %+v\nwant: %+v", rep.Totals, want)
	}
	if len(rep.ShortPaid()) != 1 || len(rep.OverPaid()) != 1 || len(rep.OK()) != 1 {
		t.Fatalf("category slices wrong: short %d, over %d, ok %d",
			len(rep.ShortPaid()), len(rep.OverPaid()), len(rep.OK()))
	}
}

func TestReportCategoriesSortWorstFirst(t *testing.T) {
	opts := testOpts()
	orders := []Order{
		{ID: "A1", GrossValue: rs(1000)},
		{ID: "A2", GrossValue: rs(1000)},
		{ID: "A3", GrossValue: rs(1000)},
	}
	e := ExpectedPayout(orders[0], opts.AgreedCommissionPct, opts.Rates).Payout
	rep := Reconcile(orders, []Settlement{
		{OrderID: "A1", NetSettled: e - rs(10)},
		{OrderID: "A2", NetSettled: e - rs(90)},
		{OrderID: "A3", NetSettled: e - rs(50)},
	}, nil, opts)

	short := rep.ShortPaid()
	if len(short) != 3 {
		t.Fatalf("got %d short-paid rows, want 3", len(short))
	}
	wantOrder := []string{"A2", "A3", "A1"} // 90, 50, 10
	for i, id := range wantOrder {
		if short[i].OrderID != id {
			t.Fatalf("short-paid position %d = %q, want %q (worst first)", i, short[i].OrderID, id)
		}
	}

	// Over-paid sorts largest first too.
	rep = Reconcile(orders, []Settlement{
		{OrderID: "A1", NetSettled: e + rs(10)},
		{OrderID: "A2", NetSettled: e + rs(90)},
		{OrderID: "A3", NetSettled: e + rs(50)},
	}, nil, opts)
	over := rep.OverPaid()
	for i, id := range []string{"A2", "A3", "A1"} {
		if over[i].OrderID != id {
			t.Fatalf("over-paid position %d = %q, want %q", i, over[i].OrderID, id)
		}
	}
}

func TestReconcileEmptyInputs(t *testing.T) {
	tests := []struct {
		name        string
		orders      []Order
		settlements []Settlement
	}{
		{name: "nothing at all"},
		{name: "orders only", orders: []Order{{ID: "A1", GrossValue: rs(100)}}},
		{name: "settlements only", settlements: []Settlement{{OrderID: "A1", NetSettled: rs(100)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Reconcile(tc.orders, tc.settlements, nil, testOpts())
			if rep.Totals.MoneyOwedBack < 0 {
				t.Fatalf("money owed back must never be negative, got %s", Money(rep.Totals.MoneyOwedBack))
			}
			if len(rep.Rows) != 0 {
				t.Fatalf("got %d reconciled rows, want 0", len(rep.Rows))
			}
		})
	}
}

func TestReconcileRollsUpDishes(t *testing.T) {
	opts := testOpts()
	orders := []Order{{
		ID: "A1", GrossValue: rs(200), PackagingCharge: rs(15),
		Items: []Item{{Name: "Biryani", Qty: 1, UnitPrice: rs(200)}},
	}}
	costs := map[string]int64{DishKey("Biryani"): rs(140)}
	rep := Reconcile(orders, []Settlement{{OrderID: "A1", NetSettled: rs(100)}}, costs, opts)
	if len(rep.Dishes) != 1 {
		t.Fatalf("got %d dishes, want 1", len(rep.Dishes))
	}
	d := rep.Dishes[0]
	if !d.CostKnown {
		t.Fatal("the food cost was supplied and must be marked known")
	}
	if !d.LossMaking {
		t.Fatalf("Biryani at 22.5%% commission plus packaging should lose money, net margin %s",
			Money(d.NetMargin))
	}
}
