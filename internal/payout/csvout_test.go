package payout

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

// sampleReport builds a report with one of everything, so the exports can be
// checked against a known shape.
func sampleReport(t *testing.T) Report {
	t.Helper()
	opts := testOpts()
	orders := []Order{
		{ID: "A1", Date: "2026-11-01", GrossValue: rs(1000), PackagingCharge: rs(20),
			Items: []Item{{Name: "Biryani", Qty: 1, UnitPrice: rs(1000)}}},
		{ID: "A2", Date: "2026-11-02", GrossValue: rs(1000)}, // short-paid and overcharged
		{ID: "A3", Date: "2026-11-03", GrossValue: rs(1000)}, // over-paid
		{ID: "A4", Date: "2026-11-04", GrossValue: rs(1000)}, // never settled
	}
	e := func(o Order) int64 { return ExpectedPayout(o, opts.AgreedCommissionPct, opts.Rates).Payout }
	settlements := []Settlement{
		{OrderID: "A1", CommissionCharged: rs(225), TaxWithheld: rs(1), NetSettled: e(orders[0])},
		{OrderID: "A2", CommissionCharged: rs(250), TaxWithheld: rs(1), NetSettled: e(orders[1]) - rs(25)},
		{OrderID: "A3", CommissionCharged: rs(225), TaxWithheld: rs(1), NetSettled: e(orders[2]) + rs(40)},
		{OrderID: "A9", NetSettled: rs(10)},
	}
	rep := Reconcile(orders, settlements, map[string]int64{DishKey("Biryani"): rs(800)}, opts)
	rep.Problems = append(rep.Problems, RowProblem{
		Source: "orders", Line: 9, Column: "gross value", Value: "oops",
		Reason: "not a readable amount; treated as zero",
	})
	return rep
}

func TestWriteExportAllKinds(t *testing.T) {
	rep := sampleReport(t)
	tests := []struct {
		kind      Export
		wantRows  int // data rows, excluding the header
		wantFirst string
	}{
		{kind: ExportShortPaid, wantRows: 1, wantFirst: "A2"},
		{kind: ExportOverPaid, wantRows: 1, wantFirst: "A3"},
		{kind: ExportOK, wantRows: 1, wantFirst: "A1"},
		{kind: ExportOvercharge, wantRows: 1, wantFirst: "A2"},
		{kind: ExportInBooksNotSettled, wantRows: 1, wantFirst: "A4"},
		{kind: ExportSettledNotInBooks, wantRows: 1, wantFirst: "A9"},
		{kind: ExportDishes, wantRows: 1, wantFirst: "Biryani"},
		{kind: ExportDisputes, wantRows: 3, wantFirst: "A2"}, // short-paid, unsettled, total
		{kind: ExportProblems, wantRows: 1, wantFirst: "orders"},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteExport(&buf, rep, tc.kind); err != nil {
				t.Fatalf("WriteExport(%s) returned %v", tc.kind, err)
			}
			recs, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
			if err != nil {
				t.Fatalf("the export is not valid CSV: %v", err)
			}
			if len(recs) != tc.wantRows+1 {
				t.Fatalf("got %d rows including the header, want %d", len(recs), tc.wantRows+1)
			}
			// Every row must have the same width as the header.
			for i, r := range recs {
				if len(r) != len(recs[0]) {
					t.Fatalf("row %d has %d cells, header has %d", i, len(r), len(recs[0]))
				}
			}
			if recs[1][0] != tc.wantFirst {
				t.Fatalf("first data row starts with %q, want %q", recs[1][0], tc.wantFirst)
			}
			if tc.kind.Title() == "" {
				t.Fatal("every export must have a title")
			}
			if !tc.kind.Valid() {
				t.Fatalf("%s should be a valid export", tc.kind)
			}
		})
	}
}

func TestWriteExportUnknownKind(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteExport(&buf, sampleReport(t), Export("nonsense")); err == nil {
		t.Fatal("an unknown export kind should be an error")
	}
	if Export("nonsense").Valid() {
		t.Fatal("an unknown export kind must not validate")
	}
	if len(Exports()) != 9 {
		t.Fatalf("Exports() listed %d kinds, want 9", len(Exports()))
	}
}

func TestDisputeListContent(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := WriteExport(&buf, rep, ExportDisputes); err != nil {
		t.Fatalf("WriteExport returned %v", err)
	}
	recs, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}

	// Row 1: the short-paid, overcharged order.
	short := recs[1]
	if short[0] != "A2" {
		t.Fatalf("first dispute row = %q, want A2", short[0])
	}
	if !strings.Contains(short[2], "overcharge") {
		t.Fatalf("issue = %q, want it to name the overcharge", short[2])
	}
	if short[12] != Decimal(rs(25)) {
		t.Fatalf("amount claimed = %q, want %q", short[12], Decimal(rs(25)))
	}
	if !strings.Contains(short[13], "Commission charged") {
		t.Fatalf("basis of claim = %q, want the commission arithmetic", short[13])
	}

	// Row 2: the order that was never settled.
	missing := recs[2]
	if missing[0] != "A4" {
		t.Fatalf("second dispute row = %q, want A4", missing[0])
	}
	if missing[2] != "No settlement received" {
		t.Fatalf("issue = %q", missing[2])
	}

	// Row 3: the total, which must equal the headline figure and must say in
	// words that the overcharge is not claimed twice.
	total := recs[3]
	if total[0] != "TOTAL" {
		t.Fatalf("last row starts with %q, want TOTAL", total[0])
	}
	if total[12] != Decimal(rep.Totals.MoneyOwedBack) {
		t.Fatalf("total claimed = %q, want %q", total[12], Decimal(rep.Totals.MoneyOwedBack))
	}
	if !strings.Contains(total[13], "not claimed twice") {
		t.Fatalf("the total should state that the overcharge is inside the short payments, got %q", total[13])
	}
}

func TestDisputeListEmptyWhenNothingIsOwed(t *testing.T) {
	opts := testOpts()
	order := Order{ID: "A1", GrossValue: rs(1000)}
	exp := ExpectedPayout(order, opts.AgreedCommissionPct, opts.Rates)
	rep := Reconcile([]Order{order},
		[]Settlement{{OrderID: "A1", CommissionCharged: rs(225), NetSettled: exp.Payout}}, nil, opts)
	if rep.Totals.MoneyOwedBack != 0 {
		t.Fatalf("money owed back = %s, want nothing", Money(rep.Totals.MoneyOwedBack))
	}
	var buf bytes.Buffer
	if err := WriteExport(&buf, rep, ExportDisputes); err != nil {
		t.Fatalf("WriteExport returned %v", err)
	}
	recs, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}
	// Header plus the total line only.
	if len(recs) != 2 {
		t.Fatalf("got %d rows, want just the header and the total", len(recs))
	}
	if recs[1][12] != "0.00" {
		t.Fatalf("total claimed = %q, want 0.00", recs[1][12])
	}
}

func TestExportAmountsAreMachineReadable(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := WriteExport(&buf, rep, ExportShortPaid); err != nil {
		t.Fatalf("WriteExport returned %v", err)
	}
	out := buf.String()
	// A spreadsheet must read these as numbers, so no grouping separators and
	// no currency symbols anywhere in the amounts.
	if strings.Contains(out, "\u20b9") {
		t.Fatal("exports must not carry a currency symbol in the cells")
	}
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}
	// Column 15 is the expected payout; it must parse as a plain decimal.
	if _, err := ParseAmount(recs[1][15]); err != nil {
		t.Fatalf("expected payout cell %q is not a plain number: %v", recs[1][15], err)
	}
	if strings.Contains(recs[1][15], ",") {
		t.Fatalf("amount cell %q must not use digit grouping", recs[1][15])
	}
}

func TestDishExportVerdicts(t *testing.T) {
	opts := Options{AgreedCommissionPct: Percent(25), Rates: RatesIndiaRestaurant()}
	orders := []Order{{
		ID: "A1", GrossValue: rs(500), PackagingCharge: rs(15),
		Items: []Item{
			{Name: "Trap Dish", Qty: 1, UnitPrice: rs(200)},
			{Name: "Good Dish", Qty: 1, UnitPrice: rs(200)},
			{Name: "No Cost Dish", Qty: 1, UnitPrice: rs(100)},
		},
	}}
	costs := map[string]int64{
		// Rs 200 menu, Rs 150 cost: a 25% gross margin, but after Rs 50 of
		// commission, Rs 6 of packaging and Rs 0.20 of TDS the kitchen keeps
		// Rs 143.80 and loses Rs 6.20 a plate.
		DishKey("Trap Dish"): rs(150),
		DishKey("Good Dish"): rs(40),
	}
	rep := Reconcile(orders, []Settlement{{OrderID: "A1", NetSettled: rs(1)}}, costs, opts)

	var buf bytes.Buffer
	if err := WriteExport(&buf, rep, ExportDishes); err != nil {
		t.Fatalf("WriteExport returned %v", err)
	}
	recs, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}
	verdicts := map[string]string{}
	for _, r := range recs[1:] {
		verdicts[r[0]] = r[len(r)-1]
	}
	if !strings.Contains(verdicts["Trap Dish"], "loses money on the platform") {
		t.Fatalf("Trap Dish verdict = %q", verdicts["Trap Dish"])
	}
	if !strings.Contains(verdicts["Good Dish"], "Profitable after commission") {
		t.Fatalf("Good Dish verdict = %q", verdicts["Good Dish"])
	}
	if !strings.Contains(verdicts["No Cost Dish"], "No food cost entered") {
		t.Fatalf("No Cost Dish verdict = %q", verdicts["No Cost Dish"])
	}
}
