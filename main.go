// Command aggregator-payout-reconciler serves a small local web interface that
// reconciles delivery-aggregator settlement statements against a restaurant's
// own order book.
//
// It is one binary with no dependencies. It listens on the loopback interface,
// keeps everything in one JSON file beside itself, and makes no outbound
// network calls of any kind.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sreenivas-sadhu-prabhakara/aggregator-payout-reconciler/internal/payout"
)

//go:embed web
var webFiles embed.FS

// maxUpload caps a single CSV body. A year of orders for a busy kitchen is a
// few megabytes; 32 MB is generous and stops a stray file exhausting memory.
const maxUpload = 32 << 20

// requestMarker is a header the browser page sets on every request that
// changes state. A page on another origin cannot set a custom header without a
// CORS preflight, which this server never grants, so requiring it stops
// another site in the same browser from driving this tool behind the owner's
// back.
const requestMarker = "X-Payout-Reconciler"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on; keep it on the loopback interface")
	data := flag.String("data", "./aggregator-payout-reconciler-data.json", "file to keep imported statements and settings in")
	flag.Parse()

	st, err := loadStore(*data)
	if err != nil {
		log.Fatalf("cannot open the data file %s: %v", *data, err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newServer(st),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *addr, err)
	}
	fmt.Printf("Aggregator payout reconciler\n")
	fmt.Printf("  open     http://%s/\n", ln.Addr().String())
	fmt.Printf("  data     %s\n", st.path)
	fmt.Printf("  offline  nothing is sent anywhere; stop with Ctrl-C\n")
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Stored state
// ---------------------------------------------------------------------------

// settings are the terms and rates the owner entered. Rates are kept as plain
// percentages so that the data file can be read, and corrected, by hand.
type settings struct {
	Aggregator                 string  `json:"aggregator"`
	CommissionPct              float64 `json:"commissionPct"`
	RateSetName                string  `json:"rateSetName"`
	GSTTCSPct                  float64 `json:"gstTcsPct"`
	TDS194OPct                 float64 `json:"tds194oPct"`
	PGFeePct                   float64 `json:"pgFeePct"`
	PackagingBorneByRestaurant bool    `json:"packagingBorneByRestaurant"`
	ToleranceRupees            float64 `json:"toleranceRupees"`
	AuditPGFee                 bool    `json:"auditPgFee"`
}

func defaultSettings() settings {
	r := payout.RatesIndiaRestaurant()
	return settings{
		CommissionPct:              0, // the owner's own agreement; never invented
		RateSetName:                r.Name,
		GSTTCSPct:                  0,
		TDS194OPct:                 0.1,
		PackagingBorneByRestaurant: true,
		ToleranceRupees:            1,
	}
}

// options turns the stored settings into the domain's run parameters.
func (s settings) options() payout.Options {
	return payout.Options{
		Aggregator:          s.Aggregator,
		AgreedCommissionPct: payout.Percent(s.CommissionPct),
		Rates: payout.RateSet{
			Name:                       s.RateSetName,
			GSTTCS:                     payout.Percent(s.GSTTCSPct),
			TDS194O:                    payout.Percent(s.TDS194OPct),
			PGFee:                      payout.Percent(s.PGFeePct),
			PackagingBorneByRestaurant: s.PackagingBorneByRestaurant,
		},
		TolerancePaise: int64(s.ToleranceRupees*100 + 0.5),
		AuditPGFee:     s.AuditPGFee,
	}
}

// fileNote records what one import produced, so the page can say what it read.
type fileNote struct {
	Name       string   `json:"name"`
	Rows       int      `json:"rows"`
	Kept       int      `json:"kept"`
	Headers    []string `json:"headers"`
	Recognised []string `json:"recognised"`
	Note       string   `json:"note,omitempty"`
	At         string   `json:"at"`
}

// stored is the whole contents of the data file.
type stored struct {
	Version            int                 `json:"version"`
	Settings           settings            `json:"settings"`
	Orders             []payout.Order      `json:"orders"`
	Settlements        []payout.Settlement `json:"settlements"`
	Costs              map[string]int64    `json:"foodCosts"`
	OrderProblems      []payout.RowProblem `json:"orderProblems"`
	SettlementProblems []payout.RowProblem `json:"settlementProblems"`
	OrderFile          *fileNote           `json:"orderFile,omitempty"`
	SettlementFile     *fileNote           `json:"settlementFile,omitempty"`
	CostFile           *fileNote           `json:"costFile,omitempty"`
}

// store is the data file plus the lock that serialises access to it.
type store struct {
	mu   sync.Mutex
	path string
	data stored
}

func loadStore(path string) (*store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s := &store{path: abs, data: stored{Version: 1, Settings: defaultSettings(), Costs: map[string]int64{}}}
	b, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return s, nil
	}
	var d stored
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("the data file is not readable JSON: %w", err)
	}
	if d.Costs == nil {
		d.Costs = map[string]int64{}
	}
	if d.Version == 0 {
		d.Version = 1
	}
	s.data = d
	return s, nil
}

// save writes the data file atomically: a temporary file in the same directory
// followed by a rename, so an interrupted write never truncates good data.
func (s *store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".payout-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

// report runs the reconciliation over whatever is currently loaded.
func (s *store) report() payout.Report {
	opts := s.data.Settings.options()
	rep := payout.Reconcile(s.data.Orders, s.data.Settlements, s.data.Costs, opts)
	// Import problems belong in the same list the page shows, so that nothing
	// the tool could not read is hidden from the owner.
	all := make([]payout.RowProblem, 0,
		len(s.data.OrderProblems)+len(s.data.SettlementProblems)+len(rep.Problems))
	all = append(all, s.data.OrderProblems...)
	all = append(all, s.data.SettlementProblems...)
	all = append(all, rep.Problems...)
	rep.Problems = all
	return rep
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

func newServer(st *store) http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatalf("the embedded interface is missing: %v", err)
	}
	files := http.FileServerFS(sub)
	mux.Handle("GET /", staticHeaders(files))

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var in settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "the settings could not be read: "+err.Error())
			return
		}
		if err := validateSettings(&in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.data.Settings = in
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the settings could not be saved: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/import/orders", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		body, name, ok := readUpload(w, r)
		if !ok {
			return
		}
		imp, err := payout.ParseOrders(strings.NewReader(body))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.data.Orders = imp.Orders
		st.data.OrderProblems = imp.Problems
		note := &fileNote{
			Name: name, Rows: imp.RowsRead, Kept: len(imp.Orders),
			Headers: imp.Headers, Recognised: imp.Recognised, At: now(),
		}
		if imp.PerItemRows {
			note.Note = "read as one row per dish line and grouped by order reference"
		} else {
			note.Note = "read as one row per order; no dish lines, so the per-dish table stays empty"
		}
		st.data.OrderFile = note
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the import could not be saved: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/import/settlements", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		body, name, ok := readUpload(w, r)
		if !ok {
			return
		}
		imp, err := payout.ParseSettlements(strings.NewReader(body))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.data.Settlements = imp.Settlements
		st.data.SettlementProblems = imp.Problems
		note := &fileNote{
			Name: name, Rows: imp.RowsRead, Kept: len(imp.Settlements),
			Headers: imp.Headers, Recognised: imp.Recognised, At: now(),
		}
		if imp.TaxFromParts {
			note.Note = "withholding summed from separate TCS and TDS columns"
		}
		st.data.SettlementFile = note
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the import could not be saved: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/import/costs", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		body, name, ok := readUpload(w, r)
		if !ok {
			return
		}
		costs, probs, err := payout.ParseCosts(strings.NewReader(body))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.data.Costs = costs
		st.data.CostFile = &fileNote{Name: name, Rows: len(costs) + len(probs), Kept: len(costs), At: now()}
		st.data.OrderProblems = append(st.data.OrderProblems, probs...)
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the food costs could not be saved: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/costs", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		var in map[string]float64
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "the food costs could not be read: "+err.Error())
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.data.Costs == nil {
			st.data.Costs = map[string]int64{}
		}
		for name, rupees := range in {
			key := payout.DishKey(name)
			if key == "" {
				continue
			}
			if rupees <= 0 {
				delete(st.data.Costs, key)
				continue
			}
			st.data.Costs[key] = int64(rupees*100 + 0.5)
		}
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the food costs could not be saved: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		keep := st.data.Settings
		st.data = stored{Version: 1, Settings: keep, Costs: map[string]int64{}}
		if err := st.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "the data file could not be cleared: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("POST /api/example", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if err := st.loadExample(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st.view())
	})

	mux.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {
		kind := payout.Export(r.URL.Query().Get("kind"))
		if !kind.Valid() {
			writeError(w, http.StatusBadRequest, "unknown export")
			return
		}
		st.mu.Lock()
		rep := st.report()
		st.mu.Unlock()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", string(kind)+".csv"))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err := payout.WriteExport(w, rep, kind); err != nil {
			log.Printf("export %s failed: %v", kind, err)
		}
	})

	return mux
}

// stateView is what the page is given: the settings, what was imported, the
// report, and the constants the page quotes in its assumptions.
type stateView struct {
	Settings    settings            `json:"settings"`
	Presets     []presetView        `json:"presets"`
	Orders      int                 `json:"orders"`
	Settlements int                 `json:"settlements"`
	Costs       map[string]int64    `json:"foodCosts"`
	Files       map[string]fileNote `json:"files"`
	Report      payout.Report       `json:"report"`
	Constants   constantsView       `json:"constants"`
	Exports     []exportView        `json:"exports"`
	DataFile    string              `json:"dataFile"`
}

type presetView struct {
	Name                       string  `json:"name"`
	GSTTCSPct                  float64 `json:"gstTcsPct"`
	TDS194OPct                 float64 `json:"tds194oPct"`
	PackagingBorneByRestaurant bool    `json:"packagingBorneByRestaurant"`
}

type constantsView struct {
	GSTTCSCeiling  string `json:"gstTcsCeiling"`
	GSTTCSNotified string `json:"gstTcsNotified"`
	TDS194O        string `json:"tds194o"`
	TDS194OOld     string `json:"tds194oOld"`
}

type exportView struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

func (s *store) view() stateView {
	v := stateView{
		Settings:    s.data.Settings,
		Orders:      len(s.data.Orders),
		Settlements: len(s.data.Settlements),
		Costs:       s.data.Costs,
		Files:       map[string]fileNote{},
		Report:      s.report(),
		DataFile:    s.path,
		Constants: constantsView{
			GSTTCSCeiling:  payout.GSTTCSSection52Ceiling.String(),
			GSTTCSNotified: payout.GSTTCSSection52Notified.String(),
			TDS194O:        payout.TDSSection194O.String(),
			TDS194OOld:     payout.TDSSection194OUntilSep2024.String(),
		},
	}
	for _, p := range payout.Presets() {
		v.Presets = append(v.Presets, presetView{
			Name:                       p.Name,
			GSTTCSPct:                  float64(p.GSTTCS) / 100,
			TDS194OPct:                 float64(p.TDS194O) / 100,
			PackagingBorneByRestaurant: p.PackagingBorneByRestaurant,
		})
	}
	for _, e := range payout.Exports() {
		v.Exports = append(v.Exports, exportView{Kind: string(e), Title: e.Title()})
	}
	if s.data.OrderFile != nil {
		v.Files["orders"] = *s.data.OrderFile
	}
	if s.data.SettlementFile != nil {
		v.Files["settlements"] = *s.data.SettlementFile
	}
	if s.data.CostFile != nil {
		v.Files["costs"] = *s.data.CostFile
	}
	return v
}

func validateSettings(in *settings) error {
	in.Aggregator = strings.TrimSpace(in.Aggregator)
	if len(in.Aggregator) > 80 {
		in.Aggregator = in.Aggregator[:80]
	}
	in.RateSetName = strings.TrimSpace(in.RateSetName)
	if len(in.RateSetName) > 120 {
		in.RateSetName = in.RateSetName[:120]
	}
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"commission", in.CommissionPct},
		{"GST TCS", in.GSTTCSPct},
		{"TDS", in.TDS194OPct},
		{"payment gateway fee", in.PGFeePct},
	} {
		if f.v < 0 || f.v > 100 {
			return fmt.Errorf("the %s rate must be between 0 and 100 percent", f.name)
		}
	}
	if in.ToleranceRupees < 0 || in.ToleranceRupees > 10000 {
		return errors.New("the tolerance must be between 0 and 10000")
	}
	return nil
}

// guard rejects a state-changing request that did not come from this tool's own
// page. See requestMarker.
func guard(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(requestMarker) == "" {
		writeError(w, http.StatusForbidden,
			"this request did not come from the tool's own page")
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) {
		writeError(w, http.StatusForbidden, "cross-origin requests are not accepted")
		return false
	}
	return true
}

func sameOrigin(origin, host string) bool {
	for _, p := range []string{"http://", "https://"} {
		if strings.TrimPrefix(origin, p) == host {
			return true
		}
	}
	return false
}

// readUpload reads a CSV body. The page sends the file's text as the request
// body with its name in a header, which needs no multipart handling.
func readUpload(w http.ResponseWriter, r *http.Request) (body, name string, ok bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge,
			"the file could not be read; it may be larger than 32 MB")
		return "", "", false
	}
	if len(b) == 0 {
		writeError(w, http.StatusBadRequest, "the file was empty")
		return "", "", false
	}
	name = strings.TrimSpace(r.Header.Get("X-File-Name"))
	if name == "" {
		name = "pasted data"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	// A file name only ever appears as text in the page, so strip anything
	// that is not safe to show.
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '<' || r == '>' || r == '"' {
			return -1
		}
		return r
	}, name)
	return string(b), name, true
}

func staticHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("could not write the response: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func now() string { return time.Now().Format("2006-01-02 15:04") }

// loadExample fills the store with a small worked example, so that the tool
// can be seen working before anyone wrestles with their own export. The
// figures are illustrative and are labelled as such in the interface.
func (s *store) loadExample() error {
	const orders = `Order #,Order Date,Item Total (INR),Merchant Discount,Platform Discount,Packing Charges,Item Name,Qty,Unit Price
#FD-1001,2026-11-01,"1,000.00",0,200.00,20.00,Hyderabadi Biryani,2,500.00
#FD-1002,2026-11-01,400.00,50.00,0,20.00,Masala Dosa,2,200.00
#FD-1003,2026-11-02,600.00,0,0,20.00,Hyderabadi Biryani,1,500.00
#FD-1003,2026-11-02,600.00,0,0,20.00,Filter Coffee,2,50.00
#FD-1004,2026-11-02,300.00,0,100.00,20.00,Masala Dosa,1,200.00
#FD-1004,2026-11-02,300.00,0,100.00,20.00,Filter Coffee,2,50.00
#FD-1005,2026-11-03,750.00,0,0,20.00,Paneer Butter Masala,1,250.00
#FD-1005,2026-11-03,750.00,0,0,20.00,Hyderabadi Biryani,1,500.00
`
	const settlements = `order_no,commission_charged,payment_gateway_fee,tcs,tds,net_payout
fd-1001,225.00,18.00,0,1.00,736.00
fd-1002,100.00,7.00,0,0.35,222.65
fd-1003,135.00,11.00,0,0.60,433.40
fd-1004,67.50,5.00,0,0.30,207.20
fd-1006,40.00,3.00,0,0.20,156.80
`
	// The biryani is the point of the example: a 21% margin on the menu card,
	// and a loss on every single plate once 22.5% commission, the packaging
	// share and the withholding come out.
	const costs = `Dish,Food cost per unit
Hyderabadi Biryani,395.00
Masala Dosa,45.00
Filter Coffee,12.00
Paneer Butter Masala,180.00
`
	oi, err := payout.ParseOrders(strings.NewReader(orders))
	if err != nil {
		return fmt.Errorf("the worked example could not be loaded: %w", err)
	}
	si, err := payout.ParseSettlements(strings.NewReader(settlements))
	if err != nil {
		return fmt.Errorf("the worked example could not be loaded: %w", err)
	}
	cm, _, err := payout.ParseCosts(strings.NewReader(costs))
	if err != nil {
		return fmt.Errorf("the worked example could not be loaded: %w", err)
	}

	st := s.data.Settings
	st.Aggregator = "Example platform"
	st.CommissionPct = 22.5
	r := payout.RatesIndiaRestaurant()
	st.RateSetName = r.Name
	st.GSTTCSPct = float64(r.GSTTCS) / 100
	st.TDS194OPct = float64(r.TDS194O) / 100
	st.PackagingBorneByRestaurant = true
	st.ToleranceRupees = 1

	s.data.Settings = st
	s.data.Orders = oi.Orders
	s.data.Settlements = si.Settlements
	s.data.Costs = cm
	s.data.OrderProblems = oi.Problems
	s.data.SettlementProblems = si.Problems
	s.data.OrderFile = &fileNote{
		Name: "worked example orders", Rows: oi.RowsRead, Kept: len(oi.Orders),
		Headers: oi.Headers, Recognised: oi.Recognised, At: now(),
		Note: "illustrative figures, not real trading data",
	}
	s.data.SettlementFile = &fileNote{
		Name: "worked example settlement", Rows: si.RowsRead, Kept: len(si.Settlements),
		Headers: si.Headers, Recognised: si.Recognised, At: now(),
		Note: "illustrative figures, not real trading data",
	}
	s.data.CostFile = &fileNote{
		Name: "worked example food costs", Rows: len(cm), Kept: len(cm), At: now(),
	}
	return s.save()
}
