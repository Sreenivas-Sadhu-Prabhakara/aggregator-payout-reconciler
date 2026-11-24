package payout

import (
	"strings"
	"testing"
)

func TestNormHeader(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Order ID", "orderid"},
		{"order_id", "orderid"},
		{"ORDER-ID", "orderid"},
		{" Order Id ", "orderid"},
		{"Order #", "order"},
		{"Net Settled (INR)", "netsettledinr"},
		{"Commission Amount", "commissionamount"},
		{"Gross Value", "grossvalue"},
		{"PG Fee", "pgfee"},
		{"\ufeffOrder ID", "orderid"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := normHeader(strings.TrimPrefix(tc.in, "\ufeff")); got != tc.want {
				t.Fatalf("normHeader(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTrimQualifier(t *testing.T) {
	tests := []struct{ in, want string }{
		{"netsettledinr", "netsettled"},
		{"commissionamount", "commission"},
		{"grossvaluers", "grossvalue"},
		{"itemtotalphp", "itemtotal"},
		{"pgfeeamt", "pgfee"},
		// Nothing to strip.
		{"orderid", ""},
		{"grossvalue", ""},
		// Must not chew a short name down to nothing.
		{"amount", ""},
		{"inr", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := trimQualifier(tc.in); got != tc.want {
				t.Fatalf("trimQualifier(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestHeaderExactMatchWinsOverStripping is the regression guard on the two-pass
// matcher: "Subtotal" names the gross value and must not be reduced to "sub".
func TestHeaderExactMatchWinsOverStripping(t *testing.T) {
	imp, err := ParseOrders(strings.NewReader("Order ID,Subtotal\nA1,1000.00\n"))
	if err != nil {
		t.Fatalf("ParseOrders returned %v", err)
	}
	if imp.Orders[0].GrossValue != rs(1000) {
		t.Fatalf("gross = %s, want 1000.00", Money(imp.Orders[0].GrossValue))
	}
}

// TestAmbiguousItemsHeaderFailsLoudly: a column headed only "Items" could be a
// dish name or an order total, so the tool refuses rather than guess.
func TestAmbiguousItemsHeaderFailsLoudly(t *testing.T) {
	_, err := ParseOrders(strings.NewReader("Order ID,Items\nA1,Biryani\n"))
	if err == nil {
		t.Fatal("an ambiguous Items column should be refused, not guessed")
	}
	if !strings.Contains(err.Error(), "Items") {
		t.Fatalf("the error should name the headers it saw, got %q", err)
	}
}

func TestParseOrdersColumnSpellings(t *testing.T) {
	// The same two orders, written five ways a real export might write them.
	tests := []struct {
		name string
		csv  string
	}{
		{
			name: "plain",
			csv: "Order ID,Date,Gross Value,Restaurant Discount,Aggregator Discount,Packaging Charge\n" +
				"A1,2026-11-01,1000.00,100.00,200.00,20.00\n" +
				"A2,2026-11-02,500.00,0,0,10.00\n",
		},
		{
			name: "snake case and underscores",
			csv: "order_id,order_date,gross_value,restaurant_discount,aggregator_discount,packaging_charge\n" +
				"A1,2026-11-01,1000,100,200,20\n" +
				"A2,2026-11-02,500,0,0,10\n",
		},
		{
			name: "shouting with currency suffixes",
			csv: "ORDER NO,ORDER DATE,ITEM TOTAL (INR),MERCHANT DISCOUNT,PLATFORM DISCOUNT,PACKING CHARGES\n" +
				"A1,2026-11-01,\"1,000.00\",100.00,200.00,20.00\n" +
				"A2,2026-11-02,500.00,-,-,10.00\n",
		},
		{
			name: "rupee signs and hash markers",
			csv: "Order #,Date,Subtotal,Restaurant Funded Discount,Platform Funded Discount,Packaging\n" +
				"#A1,2026-11-01,\u20b9 1000.00,\u20b9100.00,\u20b9200.00,\u20b920.00\n" +
				"#a 2,2026-11-02,\u20b9500.00,,,\u20b910.00\n",
		},
		{
			name: "byte order mark and trailing blank line",
			csv: "\ufeffOrder ID,Date,Gross,Rest Discount,Aggregator Disc,Packaging Fee\n" +
				"A1,2026-11-01,1000,100,200,20\n" +
				"A2,2026-11-02,500,0,0,10\n" +
				"\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			imp, err := ParseOrders(strings.NewReader(tc.csv))
			if err != nil {
				t.Fatalf("ParseOrders returned %v", err)
			}
			if len(imp.Orders) != 2 {
				t.Fatalf("got %d orders, want 2 (%+v)", len(imp.Orders), imp.Orders)
			}
			a1 := imp.Orders[0]
			if a1.ID != "A1" {
				t.Fatalf("first order id = %q, want A1", a1.ID)
			}
			if a1.GrossValue != rs(1000) {
				t.Fatalf("gross = %s, want 1000.00", Money(a1.GrossValue))
			}
			if a1.RestaurantDiscount != rs(100) {
				t.Fatalf("restaurant discount = %s, want 100.00", Money(a1.RestaurantDiscount))
			}
			if a1.AggregatorDiscount != rs(200) {
				t.Fatalf("aggregator discount = %s, want 200.00", Money(a1.AggregatorDiscount))
			}
			if a1.PackagingCharge != rs(20) {
				t.Fatalf("packaging = %s, want 20.00", Money(a1.PackagingCharge))
			}
			if a1.Date != "2026-11-01" {
				t.Fatalf("date = %q, want 2026-11-01", a1.Date)
			}
			if imp.Orders[1].ID != "A2" {
				t.Fatalf("second order id = %q, want A2", imp.Orders[1].ID)
			}
			if len(imp.Problems) != 0 {
				t.Fatalf("a clean file should report no problems, got %+v", imp.Problems)
			}
		})
	}
}

func TestParseOrdersPerItemRows(t *testing.T) {
	csv := "Order ID,Date,Gross Value,Packaging Charge,Item Name,Qty,Unit Price\n" +
		"A1,2026-11-01,320.00,20.00,Biryani,1,200.00\n" +
		"A1,2026-11-01,320.00,20.00,Dosa,1,120.00\n" +
		"A2,2026-11-02,400.00,20.00,Biryani,2,200.00\n"
	imp, err := ParseOrders(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseOrders returned %v", err)
	}
	if !imp.PerItemRows {
		t.Fatal("a file with item columns must be read as one row per item")
	}
	if len(imp.Orders) != 2 {
		t.Fatalf("got %d orders, want 2", len(imp.Orders))
	}
	if len(imp.Orders[0].Items) != 2 {
		t.Fatalf("A1 has %d items, want 2", len(imp.Orders[0].Items))
	}
	if imp.Orders[0].GrossValue != rs(320) {
		t.Fatalf("A1 gross = %s, want 320.00 (taken once, not summed per row)", Money(imp.Orders[0].GrossValue))
	}
	if imp.Orders[0].PackagingCharge != rs(20) {
		t.Fatalf("A1 packaging = %s, want 20.00 (not doubled)", Money(imp.Orders[0].PackagingCharge))
	}
	if imp.Orders[1].Items[0].Qty != 2 {
		t.Fatalf("A2 quantity = %d, want 2", imp.Orders[1].Items[0].Qty)
	}
	if len(imp.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", imp.Problems)
	}
}

func TestParseOrdersDerivesGrossFromItems(t *testing.T) {
	csv := "Order ID,Item,Quantity,Price\n" +
		"A1,Biryani,2,200.00\n" +
		"A1,Dosa,1,120.00\n"
	imp, err := ParseOrders(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseOrders returned %v", err)
	}
	if len(imp.Orders) != 1 {
		t.Fatalf("got %d orders, want 1", len(imp.Orders))
	}
	if want := rs(520); imp.Orders[0].GrossValue != want {
		t.Fatalf("derived gross = %s, want %s", Money(imp.Orders[0].GrossValue), Money(want))
	}
}

func TestParseOrdersConflictingOrderLevelValue(t *testing.T) {
	csv := "Order ID,Gross Value,Item,Qty,Price\n" +
		"A1,320.00,Biryani,1,200.00\n" +
		"A1,999.00,Dosa,1,120.00\n"
	imp, err := ParseOrders(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseOrders returned %v", err)
	}
	if imp.Orders[0].GrossValue != rs(320) {
		t.Fatalf("gross = %s, want the first value 320.00", Money(imp.Orders[0].GrossValue))
	}
	if len(imp.Problems) != 1 {
		t.Fatalf("got %d problems, want 1 conflict report", len(imp.Problems))
	}
	if imp.Problems[0].Line != 3 {
		t.Fatalf("conflict reported on line %d, want 3", imp.Problems[0].Line)
	}
	if !strings.Contains(imp.Problems[0].Reason, "conflicts") {
		t.Fatalf("problem reason = %q, want it to mention the conflict", imp.Problems[0].Reason)
	}
}

func TestParseOrdersMalformedRowsAreReportedNotFatal(t *testing.T) {
	csv := "Order ID,Date,Gross Value,Item,Qty,Price\n" +
		"A1,2026-11-01,1000.00,Biryani,1,200.00\n" +
		"A2,2026-11-02,not a number,Dosa,1,120.00\n" + // bad amount
		",2026-11-03,500.00,Idli,1,60.00\n" + // no reference
		"A4,2026-11-04,500.00,Vada,two,60.00\n" + // bad quantity
		"A5,2026-11-05,250.00\n" + // short row
		"A6,2026-11-06,300.00,Pongal,1,300.00\n"
	imp, err := ParseOrders(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("a file with bad rows must still import: %v", err)
	}
	// Every order with a usable reference survives, including the short row.
	wantIDs := []string{"A1", "A2", "A4", "A5", "A6"}
	if len(imp.Orders) != len(wantIDs) {
		t.Fatalf("got %d orders, want %d (%+v)", len(imp.Orders), len(wantIDs), imp.Orders)
	}
	for i, id := range wantIDs {
		if imp.Orders[i].ID != id {
			t.Fatalf("order %d = %q, want %q", i, imp.Orders[i].ID, id)
		}
	}
	// The bad amount became zero and was reported.
	if imp.Orders[1].GrossValue != 0 {
		t.Fatalf("unreadable gross should be zero, got %s", Money(imp.Orders[1].GrossValue))
	}
	if len(imp.Problems) < 3 {
		t.Fatalf("got %d problems, want at least 3 (bad amount, no reference, bad quantity): %+v",
			len(imp.Problems), imp.Problems)
	}
	var sawAmount, sawRef, sawQty bool
	for _, p := range imp.Problems {
		switch {
		case p.Column == "gross value" && strings.Contains(p.Reason, "readable amount"):
			sawAmount = true
		case p.Column == "order id":
			sawRef = true
		case p.Column == "quantity":
			sawQty = true
		}
	}
	if !sawAmount || !sawRef || !sawQty {
		t.Fatalf("problems missed something: amount %v, reference %v, quantity %v (%+v)",
			sawAmount, sawRef, sawQty, imp.Problems)
	}
	// Nothing in this package may panic on a malformed file; reaching here
	// without a panic is part of the assertion.
	if imp.RowsRead != 6 {
		t.Fatalf("rows read = %d, want 6", imp.RowsRead)
	}
}

func TestParseOrdersErrors(t *testing.T) {
	tests := []struct {
		name    string
		csv     string
		wantErr string
	}{
		{name: "empty file", csv: "", wantErr: "empty"},
		{
			name:    "no order reference column",
			csv:     "Date,Gross Value\n2026-11-01,1000\n",
			wantErr: "no order reference column",
		},
		{
			name:    "no gross and no items",
			csv:     "Order ID,Date\nA1,2026-11-01\n",
			wantErr: "neither a gross value column nor item columns",
		},
		{
			name:    "header only",
			csv:     "Order ID,Gross Value\n",
			wantErr: "no usable rows",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseOrders(strings.NewReader(tc.csv))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseOrdersErrorNamesTheHeaders(t *testing.T) {
	// The owner needs to be told what the tool saw, not just that it failed.
	_, err := ParseOrders(strings.NewReader("Reference,Amount Paid\nX,1\n"))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "Reference") {
		t.Fatalf("error %q should quote the headers it found", err)
	}
}

func TestParseSettlements(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		want Settlement
	}{
		{
			name: "plain",
			csv: "Order ID,Commission,PG Fee,Tax Withheld,Net Settled\n" +
				"A1,225.00,18.00,1.00,756.00\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(1), NetSettled: rs(756), Line: 2},
		},
		{
			name: "deductions written as negatives are read as deductions",
			csv: "Order ID,Commission,PG Fee,Tax Withheld,Net Settled\n" +
				"A1,-225.00,-18.00,-1.00,756.00\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(1), NetSettled: rs(756), Line: 2},
		},
		{
			name: "accounting brackets",
			csv: "Order ID,Commission,PG Fee,Tax Withheld,Net Settled\n" +
				"A1,(225.00),(18.00),(1.00),756.00\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(1), NetSettled: rs(756), Line: 2},
		},
		{
			name: "separate TCS and TDS columns are summed",
			csv: "Order No,Commission Charged,Payment Gateway Fee,TCS,TDS,Net Payout\n" +
				"A1,225.00,18.00,10.00,1.00,745.00\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(11), NetSettled: rs(745), Line: 2},
		},
		{
			name: "a combined column wins over the parts, so nothing is counted twice",
			csv: "Order ID,Commission,PG Fee,Tax Withheld,TCS,TDS,Net Settled\n" +
				"A1,225.00,18.00,11.00,10.00,1.00,745.00\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(11), NetSettled: rs(745), Line: 2},
		},
		{
			name: "alternative spellings and a hash marker",
			csv: "Order Number,Platform Fee,PG Charges,Taxes,Amount Settled\n" +
				"#a 1,225.00,18.00,1.00,\"756.00\"\n",
			want: Settlement{OrderID: "A1", CommissionCharged: rs(225), PGFee: rs(18), TaxWithheld: rs(1), NetSettled: rs(756), Line: 2},
		},
		{
			name: "a negative net payout is a recovery and keeps its sign",
			csv: "Order ID,Commission,Net Settled\n" +
				"A1,0,-50.00\n",
			want: Settlement{OrderID: "A1", NetSettled: -rs(50), Line: 2},
		},
		{
			name: "missing optional columns default to zero",
			csv: "Order ID,Net Settled\n" +
				"A1,756.00\n",
			want: Settlement{OrderID: "A1", NetSettled: rs(756), Line: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			imp, err := ParseSettlements(strings.NewReader(tc.csv))
			if err != nil {
				t.Fatalf("ParseSettlements returned %v", err)
			}
			if len(imp.Settlements) != 1 {
				t.Fatalf("got %d settlements, want 1", len(imp.Settlements))
			}
			if imp.Settlements[0] != tc.want {
				t.Fatalf("settlement mismatch\n got: %+v\nwant: %+v", imp.Settlements[0], tc.want)
			}
		})
	}
}

func TestParseSettlementsMalformedRows(t *testing.T) {
	csv := "Order ID,Commission,Net Settled\n" +
		"A1,225.00,756.00\n" +
		",225.00,756.00\n" + // no reference
		"A3,oops,500.00\n" + // bad commission
		"\n" + // blank line
		"A5,225.00,756.00\n"
	imp, err := ParseSettlements(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("a file with bad rows must still import: %v", err)
	}
	if len(imp.Settlements) != 3 {
		t.Fatalf("got %d settlements, want 3 (%+v)", len(imp.Settlements), imp.Settlements)
	}
	if imp.Settlements[1].CommissionCharged != 0 {
		t.Fatalf("unreadable commission should be zero, got %s", Money(imp.Settlements[1].CommissionCharged))
	}
	if len(imp.Problems) != 2 {
		t.Fatalf("got %d problems, want 2: %+v", len(imp.Problems), imp.Problems)
	}
	if imp.RowsRead != 4 {
		t.Fatalf("rows read = %d, want 4 (the blank line is not a row)", imp.RowsRead)
	}
}

func TestParseSettlementsErrors(t *testing.T) {
	tests := []struct {
		name    string
		csv     string
		wantErr string
	}{
		{name: "empty", csv: "", wantErr: "empty"},
		{
			name:    "no order reference",
			csv:     "Commission,Net Settled\n225,756\n",
			wantErr: "no order reference column",
		},
		{
			name:    "no net settled column",
			csv:     "Order ID,Commission\nA1,225\n",
			wantErr: "no net settled or payout column",
		},
		{
			name:    "header only",
			csv:     "Order ID,Net Settled\n",
			wantErr: "no usable rows",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSettlements(strings.NewReader(tc.csv))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseSettlementsRaggedAndQuoted(t *testing.T) {
	// Ragged rows, a stray quote and Windows line endings must not stop the
	// import: encoding/csv is set up to tolerate them.
	csv := "Order ID,Commission,Net Settled,Notes\r\n" +
		"A1,225.00,756.00\r\n" +
		"A2,225.00,756.00,\"a note, with a comma\"\r\n" +
		"A3,225.00,756.00,unterminated \" quote\r\n"
	imp, err := ParseSettlements(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("ParseSettlements returned %v", err)
	}
	if len(imp.Settlements) != 3 {
		t.Fatalf("got %d settlements, want 3 (%+v)", len(imp.Settlements), imp.Settlements)
	}
	for _, s := range imp.Settlements {
		if s.NetSettled != rs(756) {
			t.Fatalf("%s net = %s, want 756.00", s.OrderID, Money(s.NetSettled))
		}
	}
}

func TestParseCosts(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		want map[string]int64
	}{
		{
			name: "with a header",
			csv:  "Dish,Food Cost\nBiryani,140.00\nDosa,30.00\n",
			want: map[string]int64{"BIRYANI": rs(140), "DOSA": rs(30)},
		},
		{
			name: "without a header",
			csv:  "Biryani,140.00\nDosa,30.00\n",
			want: map[string]int64{"BIRYANI": rs(140), "DOSA": rs(30)},
		},
		{
			name: "names are folded and extra columns ignored",
			csv:  "Dish,Cost,Notes\n  biryani  ,140.00,anything\n",
			want: map[string]int64{"BIRYANI": rs(140)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := ParseCosts(strings.NewReader(tc.csv))
			if err != nil {
				t.Fatalf("ParseCosts returned %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d costs, want %d (%+v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("cost of %q = %s, want %s", k, Money(got[k]), Money(v))
				}
			}
		})
	}
}

func TestParseCostsBadRows(t *testing.T) {
	got, probs, err := ParseCosts(strings.NewReader("Dish,Cost\nBiryani,140.00\nDosa,free\n,50.00\n"))
	if err != nil {
		t.Fatalf("ParseCosts returned %v", err)
	}
	if len(got) != 1 || got["BIRYANI"] != rs(140) {
		t.Fatalf("got %+v, want only Biryani", got)
	}
	if len(probs) != 1 {
		t.Fatalf("got %d problems, want 1: %+v", len(probs), probs)
	}
}

func TestParseCostsEmpty(t *testing.T) {
	if _, _, err := ParseCosts(strings.NewReader("")); err == nil {
		t.Fatal("an empty cost file should be an error")
	}
}

// TestEndToEndImportAndReconcile walks both files through the whole pipeline,
// which is what the web UI does.
func TestEndToEndImportAndReconcile(t *testing.T) {
	orderCSV := "Order #,Order Date,Item Total (INR),Merchant Discount,Platform Discount,Packing Charges,Item Name,Qty,Unit Price\n" +
		"#A-101,2026-11-01,\"1,000.00\",0,200.00,20.00,Hyderabadi Biryani,2,500.00\n" +
		"#A-102,2026-11-02,400.00,50.00,0,20.00,Masala Dosa,2,200.00\n" +
		"#A-103,2026-11-03,600.00,0,0,20.00,Hyderabadi Biryani,1,600.00\n"
	// The statement writes the same references in its own style: lower case,
	// a hash marker, a stray space. The hyphens survive normalisation on both
	// sides, which is what lets the two files match.
	settlementCSV := "order_no,commission_charged,payment_gateway_fee,tcs,tds,net_payout\n" +
		"a-101,225.00,0,0,1.00,754.00\n" + // correct at 22.5%
		"# a-102,100.00,0,0,0.35,249.65\n" + // 100 of a 350 base is 28.57%
		"\n" // A-103 never settled

	oi, err := ParseOrders(strings.NewReader(orderCSV))
	if err != nil {
		t.Fatalf("ParseOrders: %v", err)
	}
	si, err := ParseSettlements(strings.NewReader(settlementCSV))
	if err != nil {
		t.Fatalf("ParseSettlements: %v", err)
	}
	if len(oi.Orders) != 3 {
		t.Fatalf("got %d orders, want 3", len(oi.Orders))
	}
	if len(si.Settlements) != 2 {
		t.Fatalf("got %d settlements, want 2", len(si.Settlements))
	}
	if !si.TaxFromParts {
		t.Fatal("the statement split TCS and TDS, which should be reported")
	}

	opts := testOpts()
	costs := map[string]int64{
		DishKey("Hyderabadi Biryani"): rs(400),
		DishKey("Masala Dosa"):        rs(40),
	}
	rep := Reconcile(oi.Orders, si.Settlements, costs, opts)

	// A-101 matched through normalisation despite "#A-101" and "a-101".
	if rep.Totals.Matched != 2 {
		t.Fatalf("matched = %d, want 2", rep.Totals.Matched)
	}
	if rep.Totals.OKCount != 1 {
		t.Fatalf("OK = %d, want 1 (A-101 was settled correctly)", rep.Totals.OKCount)
	}
	if rep.Totals.ShortPaidCount != 1 {
		t.Fatalf("short paid = %d, want 1 (A-102 was overcharged commission)", rep.Totals.ShortPaidCount)
	}
	if rep.Totals.OverchargeCount != 1 {
		t.Fatalf("overcharges = %d, want 1", rep.Totals.OverchargeCount)
	}
	if rep.Totals.InBooksNotSettledCount != 1 {
		t.Fatalf("unsettled = %d, want 1 (A-103)", rep.Totals.InBooksNotSettledCount)
	}
	if rep.InBooksNotSettled[0].Order.ID != "A-103" {
		t.Fatalf("unsettled order = %q, want A-103", rep.InBooksNotSettled[0].Order.ID)
	}
	if rep.Totals.MoneyOwedBack <= 0 {
		t.Fatalf("money owed back = %s, want a positive claim", Money(rep.Totals.MoneyOwedBack))
	}
	// The hyphen is preserved, so the two references did not collide.
	for _, row := range rep.Rows {
		if row.OrderID == "A101" {
			t.Fatal("the hyphen was stripped from the reference")
		}
	}
	// The biryani at Rs 400 of food cost against a Rs 500 menu price loses
	// money once 22.5% commission comes off.
	if len(rep.Dishes) != 2 {
		t.Fatalf("got %d dishes, want 2", len(rep.Dishes))
	}
	if rep.Dishes[0].Name != "Hyderabadi Biryani" {
		t.Fatalf("worst dish = %q, want Hyderabadi Biryani", rep.Dishes[0].Name)
	}
	if !rep.Dishes[0].FlattersToDeceive {
		t.Fatalf("the biryani should look profitable gross and lose money net: gross %s, net %s",
			Money(rep.Dishes[0].GrossMargin), Money(rep.Dishes[0].NetMargin))
	}
}
