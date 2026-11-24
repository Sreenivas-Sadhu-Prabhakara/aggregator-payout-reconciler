package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sreenivas-sadhu-prabhakara/aggregator-payout-reconciler/internal/payout"
)

func newTestStore(t *testing.T) *store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := loadStore(path)
	if err != nil {
		t.Fatalf("loadStore: %v", err)
	}
	return st
}

func TestLoadStoreDefaults(t *testing.T) {
	st := newTestStore(t)
	want := defaultSettings()
	if st.data.Settings != want {
		t.Fatalf("settings = %+v, want %+v", st.data.Settings, want)
	}
	// The default rate set must be the one that fits a restaurant: no TCS,
	// because the platform pays that GST itself under section 9(5).
	if want.GSTTCSPct != 0 {
		t.Fatalf("default GST TCS = %v, want 0 for restaurant service", want.GSTTCSPct)
	}
	if want.TDS194OPct != 0.1 {
		t.Fatalf("default TDS = %v, want 0.1", want.TDS194OPct)
	}
	// Commission must never be invented for the owner.
	if want.CommissionPct != 0 {
		t.Fatalf("default commission = %v, want 0", want.CommissionPct)
	}
}

func TestLoadStoreTolerantOfOddFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "empty file falls back to defaults", content: ""},
		{name: "whitespace only falls back to defaults", content: "  \n "},
		{name: "a saved file is read back", content: `{"version":1,"settings":{"commissionPct":22.5}}`},
		{name: "null costs map is replaced", content: `{"version":1,"foodCosts":null}`},
		{name: "no version is treated as version 1", content: `{"settings":{}}`},
		{name: "broken JSON is an error", content: `{not json`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := loadStore(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadStore: %v", err)
			}
			if st.data.Costs == nil {
				t.Fatal("the food cost map must never be nil")
			}
			if st.data.Version != 1 {
				t.Fatalf("version = %d, want 1", st.data.Version)
			}
		})
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	st := newTestStore(t)
	st.data.Settings.CommissionPct = 22.5
	if err := st.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("data file mode = %v, want 0600 — it holds the owner's trading data", perm)
	}
	// No temporary files may be left behind.
	entries, err := os.ReadDir(filepath.Dir(st.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".payout-") {
			t.Fatalf("a temporary file was left behind: %s", e.Name())
		}
	}
	// And it must read back.
	again, err := loadStore(st.path)
	if err != nil {
		t.Fatalf("loadStore after save: %v", err)
	}
	if again.data.Settings.CommissionPct != 22.5 {
		t.Fatalf("commission read back as %v, want 22.5", again.data.Settings.CommissionPct)
	}
}

func TestSettingsToOptions(t *testing.T) {
	s := settings{
		Aggregator:                 "Platform",
		CommissionPct:              22.5,
		GSTTCSPct:                  1,
		TDS194OPct:                 0.1,
		PGFeePct:                   1.8,
		PackagingBorneByRestaurant: true,
		ToleranceRupees:            1.5,
		AuditPGFee:                 true,
	}
	o := s.options()
	tests := []struct {
		name      string
		got, want int64
	}{
		{"commission", int64(o.AgreedCommissionPct), 2250},
		{"GST TCS", int64(o.Rates.GSTTCS), 100},
		{"TDS", int64(o.Rates.TDS194O), 10},
		{"PG fee", int64(o.Rates.PGFee), 180},
		{"tolerance in paise", o.TolerancePaise, 150},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("%s = %d, want %d", tc.name, tc.got, tc.want)
			}
		})
	}
	if !o.AuditPGFee || !o.Rates.PackagingBorneByRestaurant {
		t.Fatal("the flags did not survive conversion")
	}
}

func TestValidateSettings(t *testing.T) {
	tests := []struct {
		name    string
		in      settings
		wantErr bool
	}{
		{name: "zero is fine", in: settings{}},
		{name: "ordinary terms", in: settings{CommissionPct: 22.5, TDS194OPct: 0.1, ToleranceRupees: 1}},
		{name: "100 percent is the limit", in: settings{CommissionPct: 100}},
		{name: "negative commission", in: settings{CommissionPct: -1}, wantErr: true},
		{name: "commission over 100", in: settings{CommissionPct: 101}, wantErr: true},
		{name: "negative TCS", in: settings{GSTTCSPct: -0.5}, wantErr: true},
		{name: "TDS over 100", in: settings{TDS194OPct: 200}, wantErr: true},
		{name: "gateway fee over 100", in: settings{PGFeePct: 101}, wantErr: true},
		{name: "negative tolerance", in: settings{ToleranceRupees: -1}, wantErr: true},
		{name: "absurd tolerance", in: settings{ToleranceRupees: 10001}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			err := validateSettings(&in)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateSettings error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateSettingsTrimsLongText(t *testing.T) {
	in := settings{
		Aggregator:  strings.Repeat("x", 200),
		RateSetName: strings.Repeat("y", 300),
	}
	if err := validateSettings(&in); err != nil {
		t.Fatalf("validateSettings: %v", err)
	}
	if len(in.Aggregator) != 80 {
		t.Fatalf("aggregator kept %d characters, want 80", len(in.Aggregator))
	}
	if len(in.RateSetName) != 120 {
		t.Fatalf("rate set name kept %d characters, want 120", len(in.RateSetName))
	}
}

func TestGuardRejectsRequestsNotFromThePage(t *testing.T) {
	st := newTestStore(t)
	srv := newServer(st)
	tests := []struct {
		name     string
		marker   string
		origin   string
		host     string
		wantCode int
	}{
		{name: "no marker is refused", wantCode: http.StatusForbidden},
		{name: "marker from the same origin is accepted", marker: "1", wantCode: http.StatusOK},
		{
			name:   "marker with a foreign origin is refused",
			marker: "1", origin: "https://example.invalid", wantCode: http.StatusForbidden,
		},
		{
			name:   "marker with this origin is accepted",
			marker: "1", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", wantCode: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/reset", nil)
			if tc.marker != "" {
				r.Header.Set(requestMarker, tc.marker)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				r.Host = tc.host
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

func TestImportAndReconcileOverHTTP(t *testing.T) {
	st := newTestStore(t)
	srv := newServer(st)

	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(requestMarker, "1")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	// Terms: 22.5% commission, restaurant-service rates.
	terms, _ := json.Marshal(settings{
		Aggregator: "Platform", CommissionPct: 22.5,
		RateSetName: payout.RatesIndiaRestaurant().Name,
		TDS194OPct:  0.1, PackagingBorneByRestaurant: true, ToleranceRupees: 1,
	})
	if w := do("POST", "/api/settings", string(terms), nil); w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}

	orders := "Order ID,Date,Gross Value,Packaging Charge\nA1,2026-11-01,1000.00,20.00\nA2,2026-11-02,500.00,0\n"
	if w := do("POST", "/api/import/orders", orders, map[string]string{"X-File-Name": "orders.csv"}); w.Code != http.StatusOK {
		t.Fatalf("import orders: %d %s", w.Code, w.Body.String())
	}
	// A1 expected: 1000 - 225 - 20 - 1.00 = 754.00; settle 700 to be short.
	settlements := "Order ID,Commission,Net Settled\nA1,225.00,700.00\n"
	w := do("POST", "/api/import/settlements", settlements, map[string]string{"X-File-Name": "settlement.csv"})
	if w.Code != http.StatusOK {
		t.Fatalf("import settlements: %d %s", w.Code, w.Body.String())
	}

	var v stateView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not a state view: %v", err)
	}
	if v.Orders != 2 || v.Settlements != 1 {
		t.Fatalf("orders = %d, settlements = %d, want 2 and 1", v.Orders, v.Settlements)
	}
	if v.Report.Totals.ShortPaidCount != 1 {
		t.Fatalf("short-paid = %d, want 1", v.Report.Totals.ShortPaidCount)
	}
	if v.Report.Totals.TotalShortPaid != 5400 {
		t.Fatalf("short-paid total = %s, want 54.00", payout.Money(v.Report.Totals.TotalShortPaid))
	}
	if v.Report.Totals.InBooksNotSettledCount != 1 {
		t.Fatalf("unsettled = %d, want 1", v.Report.Totals.InBooksNotSettledCount)
	}
	if v.Files["orders"].Name != "orders.csv" {
		t.Fatalf("order file name = %q", v.Files["orders"].Name)
	}
	if v.Constants.TDS194O != "0.10%" {
		t.Fatalf("the page is told TDS is %q, want 0.10%%", v.Constants.TDS194O)
	}
	if len(v.Presets) != 4 || len(v.Exports) != 9 {
		t.Fatalf("presets = %d, exports = %d, want 4 and 9", len(v.Presets), len(v.Exports))
	}

	// The export endpoint returns CSV for the dispute list.
	r := httptest.NewRequest("GET", "/api/export?kind=dispute-list", nil)
	ew := httptest.NewRecorder()
	srv.ServeHTTP(ew, r)
	if ew.Code != http.StatusOK {
		t.Fatalf("export: %d", ew.Code)
	}
	if ct := ew.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("export content type = %q", ct)
	}
	if !strings.Contains(ew.Body.String(), "A1") || !strings.Contains(ew.Body.String(), "TOTAL") {
		t.Fatalf("the dispute list is missing rows:\n%s", ew.Body.String())
	}

	// Reset keeps the terms and drops the data.
	rw := do("POST", "/api/reset", "", nil)
	if rw.Code != http.StatusOK {
		t.Fatalf("reset: %d", rw.Code)
	}
	var after stateView
	if err := json.Unmarshal(rw.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Orders != 0 || after.Settlements != 0 {
		t.Fatalf("reset left %d orders and %d settlements", after.Orders, after.Settlements)
	}
	if after.Settings.CommissionPct != 22.5 {
		t.Fatalf("reset lost the terms: commission %v", after.Settings.CommissionPct)
	}
}

func TestImportRejectsUnusableFiles(t *testing.T) {
	st := newTestStore(t)
	srv := newServer(st)
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "empty order file", path: "/api/import/orders", body: ""},
		{name: "order file with no reference column", path: "/api/import/orders", body: "Date,Gross\n2026-01-01,100\n"},
		{name: "empty settlement file", path: "/api/import/settlements", body: ""},
		{name: "settlement file with no payout column", path: "/api/import/settlements", body: "Order ID,Commission\nA1,10\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			r.Header.Set(requestMarker, "1")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", w.Code, w.Body.String())
			}
			var e map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
				t.Fatalf("the error is not JSON: %v", err)
			}
			if e["error"] == "" {
				t.Fatal("an error response must explain itself")
			}
		})
	}
}

func TestWorkedExampleShowsTheInsight(t *testing.T) {
	st := newTestStore(t)
	if err := st.loadExample(); err != nil {
		t.Fatalf("loadExample: %v", err)
	}
	rep := st.report()
	if len(rep.Problems) != 0 {
		t.Fatalf("the worked example should import cleanly, got %+v", rep.Problems)
	}
	tot := rep.Totals
	if tot.OKCount == 0 || tot.ShortPaidCount == 0 ||
		tot.InBooksNotSettledCount == 0 || tot.SettledNotInBooksCount == 0 || tot.OverchargeCount == 0 {
		t.Fatalf("the example should show one of every category, got %+v", tot)
	}
	// The headline must not double-count, and in this example the whole
	// shortfall IS the commission overcharge.
	if tot.MoneyOwedBack != tot.TotalShortPaid+tot.InBooksNotSettledValue {
		t.Fatalf("money owed back %s does not equal short %s plus unsettled %s",
			payout.Money(tot.MoneyOwedBack), payout.Money(tot.TotalShortPaid),
			payout.Money(tot.InBooksNotSettledValue))
	}
	if tot.TotalShortPaid != tot.TotalCommissionOvercharge {
		t.Fatalf("the example's shortfall %s should be exactly the overcharge %s",
			payout.Money(tot.TotalShortPaid), payout.Money(tot.TotalCommissionOvercharge))
	}
	// And the worst dish must be the one that looks profitable and is not.
	if len(rep.Dishes) == 0 {
		t.Fatal("the example should produce a dish table")
	}
	worst := rep.Dishes[0]
	if !worst.FlattersToDeceive {
		t.Fatalf("the worst dish %q should look profitable gross and lose money net: gross %s, net %s",
			worst.Name, payout.Money(worst.GrossMargin), payout.Money(worst.NetMargin))
	}
	if worst.GrossMarginPct <= 0 || worst.NetMargin >= 0 {
		t.Fatalf("worst dish economics are wrong: %+v", worst)
	}
}

func TestExportEndpointRejectsUnknownKind(t *testing.T) {
	srv := newServer(newTestStore(t))
	r := httptest.NewRequest("GET", "/api/export?kind=../etc/passwd", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEmbeddedPageIsServed(t *testing.T) {
	srv := newServer(newTestStore(t))
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css", "/assets/accent.css", "/assets/theme-init.js", "/assets/favicon.svg"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Fatalf("GET %s served nothing", path)
		}
	}
}

func TestCostsEndpointAcceptsAndRemoves(t *testing.T) {
	st := newTestStore(t)
	srv := newServer(st)
	send := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/costs", strings.NewReader(body))
		r.Header.Set(requestMarker, "1")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	if w := send(`{"Biryani": 140.50}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}
	if got := st.data.Costs[payout.DishKey("Biryani")]; got != 14050 {
		t.Fatalf("cost = %d paise, want 14050", got)
	}
	// Zero or negative removes the dish rather than storing nonsense.
	if w := send(`{"biryani": 0}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if _, ok := st.data.Costs[payout.DishKey("Biryani")]; ok {
		t.Fatal("a zero cost should remove the dish")
	}
}
