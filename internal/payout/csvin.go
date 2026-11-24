package payout

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// RowProblem records one thing an import could not make sense of. Nothing is
// ever dropped quietly: a row that cannot be read becomes a problem the UI
// shows, with the line number and the offending cell.
type RowProblem struct {
	// Source is "orders" or "settlements".
	Source string `json:"source"`
	// Line is the 1-based line number in the file, counting the header.
	Line   int    `json:"line"`
	Column string `json:"column,omitempty"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
}

func (p RowProblem) String() string {
	s := fmt.Sprintf("line %d", p.Line)
	if p.Column != "" {
		s += fmt.Sprintf(", column %q", p.Column)
	}
	if p.Value != "" {
		s += fmt.Sprintf(" (%q)", p.Value)
	}
	return s + ": " + p.Reason
}

// normHeader reduces a header cell to a comparison key: lower case, with
// every space, underscore, hyphen, dot, bracket, slash and currency marker
// removed. "Order ID", "order_id", "ORDER-ID" and "Order Id " all become
// "orderid", so a column is recognised however the export spelled it.
func normHeader(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			// Everything else is punctuation or spacing; drop it.
		}
	}
	return b.String()
}

// trimQualifier drops a trailing currency or "amount" qualifier, so that
// "Item Total (INR)" and "Commission Amount" reduce to "itemtotal" and
// "commission". It returns "" when there is nothing to drop.
//
// It is only ever tried after the unqualified header has failed to match any
// known column, because stripping first would damage real names: "Subtotal"
// would become "sub".
func trimQualifier(k string) string {
	for _, suf := range []string{"inr", "php", "rs", "amount", "amt"} {
		if strings.HasSuffix(k, suf) {
			if trimmed := strings.TrimSuffix(k, suf); len(trimmed) >= 3 {
				return trimmed
			}
		}
	}
	return ""
}

// column binds one logical field to every header spelling seen in the wild.
type column struct {
	field   string
	aliases []string
}

var orderColumns = []column{
	{"order id", []string{"orderid", "order", "orderno", "ordernumber", "orderref", "orderreference", "id", "invoiceno", "invoicenumber", "billno"}},
	{"date", []string{"date", "orderdate", "createdat", "createdon", "orderplacedat", "invoicedate", "billdate", "orderedat"}},
	// "items" is deliberately absent: a column headed "Items" is as likely to
	// hold dish names as an order total, and a guess either way loses money
	// silently. Such a file fails loudly instead, naming its headers.
	{"gross value", []string{"gross", "grossvalue", "grossorder", "itemtotal", "itemstotal", "subtotal", "ordersub", "ordervalue", "foodvalue", "menuvalue", "netbill", "grosssale", "grosssales"}},
	{"restaurant discount", []string{"restaurantdiscount", "restdiscount", "merchantdiscount", "restaurantfundeddiscount", "merchantfundeddiscount", "discountrestaurant", "restaurantfunded", "merchantfunded", "restaurantpromo", "restaurantdisc"}},
	{"aggregator discount", []string{"aggregatordiscount", "platformdiscount", "aggregatorfundeddiscount", "platformfundeddiscount", "discountaggregator", "discountplatform", "aggregatorfunded", "platformfunded", "platformpromo", "aggregatordisc"}},
	{"packaging charge", []string{"packaging", "packagingcharge", "packagingcharges", "packing", "packingcharge", "packingcharges", "containercharge", "packagingfee"}},
	{"item name", []string{"item", "itemname", "dish", "dishname", "product", "productname", "menuitem", "description"}},
	{"quantity", []string{"qty", "quantity", "units", "nos", "count", "itemqty", "itemquantity"}},
	{"unit price", []string{"unitprice", "price", "rate", "itemprice", "mrp", "sellingprice", "unitrate", "menuprice"}},
}

var settlementColumns = []column{
	{"order id", []string{"orderid", "order", "orderno", "ordernumber", "orderref", "orderreference", "id", "invoiceno", "invoicenumber"}},
	{"commission", []string{"commission", "commissioncharged", "commissioncharge", "comm", "commissiondeducted", "servicefee", "platformfee", "platformcommission", "aggregatorcommission"}},
	{"pg fee", []string{"pgfee", "pg", "pgcharge", "pgcharges", "paymentgateway", "paymentgatewayfee", "paymentgatewaycharge", "gatewayfee", "paymentmechanismfee", "pgfeecharged", "paymentfee"}},
	{"tax withheld", []string{"taxwithheld", "taxdeducted", "taxes", "tax", "withholding", "withheld", "taxcollected"}},
	{"tcs", []string{"tcs", "gsttcs", "tcsgst", "tcscollected", "tcsdeducted"}},
	{"tds", []string{"tds", "tds194o", "incometaxtds", "tdsdeducted", "tds194"}},
	{"net settled", []string{"netsettled", "net", "netpayout", "payout", "netpayable", "settlement", "settled", "amountsettled", "settledamount", "netreceivable", "payableamount", "netsettlement"}},
}

// headerIndex maps each logical field to the position of its column.
type headerIndex struct {
	pos  map[string]int
	raw  []string
	kind string
}

func (h headerIndex) has(field string) bool { _, ok := h.pos[field]; return ok }

// cell returns the value of one logical field in a record, or "" when the
// column is absent or the row is short. A short row is a fact of life in
// exports and must not be fatal.
func (h headerIndex) cell(rec []string, field string) string {
	i, ok := h.pos[field]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// buildHeaderIndex binds the file's header cells to logical fields.
//
// It matches in two passes: first the header exactly as normalised, and only
// then the header with a trailing currency or "amount" qualifier removed. A
// header that names a field outright always wins over one that only matches
// after a suffix is stripped, so "Subtotal" is a subtotal and never a "sub".
func buildHeaderIndex(raw []string, cols []column, kind string) headerIndex {
	h := headerIndex{pos: map[string]int{}, raw: raw, kind: kind}
	keys := make([]string, len(raw))
	alt := make([]string, len(raw))
	for i, c := range raw {
		keys[i] = normHeader(strings.TrimPrefix(c, "\ufeff"))
		alt[i] = trimQualifier(keys[i])
	}
	find := func(in []string, alias string) int {
		for i, k := range in {
			if k != "" && k == alias {
				return i
			}
		}
		return -1
	}
	for _, col := range cols {
		for _, a := range col.aliases {
			matched := find(keys, a)
			if matched < 0 {
				matched = find(alt, a)
			}
			if matched >= 0 {
				if _, taken := h.pos[col.field]; !taken {
					h.pos[col.field] = matched
				}
				break
			}
		}
	}
	return h
}

// knownFields lists the logical fields found, for the UI's "columns we
// recognised" line.
func (h headerIndex) knownFields() []string {
	out := make([]string, 0, len(h.pos))
	for f := range h.pos {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// OrderImport is the result of reading an order export.
type OrderImport struct {
	Orders   []Order      `json:"orders"`
	Problems []RowProblem `json:"problems"`
	// Headers are the header cells as the file wrote them.
	Headers []string `json:"headers"`
	// Recognised lists the logical fields the header was matched to.
	Recognised []string `json:"recognised"`
	// PerItemRows is true when the file was read as one row per dish line and
	// grouped by order reference, rather than one row per order.
	PerItemRows bool `json:"perItemRows"`
	// RowsRead counts the data rows in the file.
	RowsRead int `json:"rowsRead"`
}

func newReader(r io.Reader) *csv.Reader {
	cr := csv.NewReader(r)
	// Exports are not well-formed CSV. Accept ragged rows and stray quotes
	// rather than refusing the file.
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	cr.ReuseRecord = false
	return cr
}

// readAll pulls every record it can, turning a parse failure into a problem on
// that line instead of abandoning the file.
func readAll(cr *csv.Reader, kind string) ([][]string, []RowProblem) {
	var recs [][]string
	var probs []RowProblem
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			line := 0
			if errors.As(err, &pe) {
				line = pe.Line
			}
			probs = append(probs, RowProblem{
				Source: kind, Line: line,
				Reason: "row could not be read as CSV and was skipped",
			})
			if len(recs) > 20000 {
				break
			}
			continue
		}
		recs = append(recs, rec)
	}
	return recs, probs
}

func blank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// ParseOrders reads a restaurant's order export.
//
// The file may be one row per order, or one row per dish line with the order
// reference repeated — both shapes are common, and the shape is detected from
// the header. In the per-item shape the order-level amounts are taken from the
// first row of each order that carries them; a later row carrying a different
// non-empty value is reported as a conflict and the first value is kept. Where
// no gross value column exists at all, the gross is the sum of the item lines.
//
// A malformed cell never stops the import: the row is recorded as a problem
// with its line number and the rest of the file is read.
func ParseOrders(r io.Reader) (OrderImport, error) {
	const kind = "orders"
	out := OrderImport{}
	cr := newReader(r)
	recs, probs := readAll(cr, kind)
	out.Problems = append(out.Problems, probs...)
	if len(recs) == 0 {
		return out, errors.New("the order file is empty")
	}
	h := buildHeaderIndex(recs[0], orderColumns, kind)
	out.Headers = recs[0]
	out.Recognised = h.knownFields()
	if !h.has("order id") {
		return out, fmt.Errorf("the order file has no order reference column; its headers are: %s",
			strings.Join(out.Headers, ", "))
	}
	hasItems := h.has("item name")
	hasGross := h.has("gross value")
	if !hasItems && !hasGross {
		return out, fmt.Errorf("the order file has neither a gross value column nor item columns; its headers are: %s",
			strings.Join(out.Headers, ", "))
	}
	out.PerItemRows = hasItems

	// money reads one amount cell, reporting a bad value and returning 0.
	money := func(line int, field, raw string) int64 {
		if raw == "" {
			return 0
		}
		v, err := ParseAmount(raw)
		if err != nil {
			out.Problems = append(out.Problems, RowProblem{
				Source: kind, Line: line, Column: field, Value: raw,
				Reason: "not a readable amount; treated as zero",
			})
			return 0
		}
		return v
	}

	type pending struct {
		o        Order
		setGross bool
		setRest  bool
		setAgg   bool
		setPack  bool
	}
	byID := map[string]*pending{}
	var ids []string

	for n, rec := range recs[1:] {
		line := n + 2
		if blank(rec) {
			continue
		}
		out.RowsRead++
		rawID := h.cell(rec, "order id")
		id := NormaliseOrderID(rawID)
		if id == "" {
			out.Problems = append(out.Problems, RowProblem{
				Source: kind, Line: line, Column: "order id",
				Reason: "row has no order reference and was skipped",
			})
			continue
		}
		p, ok := byID[id]
		if !ok {
			p = &pending{o: Order{ID: id, Line: line}}
			byID[id] = p
			ids = append(ids, id)
		}
		if d := h.cell(rec, "date"); d != "" && p.o.Date == "" {
			p.o.Date = d
		}

		// Order-level amounts: first non-empty wins, conflicts are reported.
		set := func(field string, dst *int64, already *bool) {
			raw := h.cell(rec, field)
			if raw == "" {
				return
			}
			v := money(line, field, raw)
			if !*already {
				*dst, *already = v, true
				return
			}
			if v != *dst {
				out.Problems = append(out.Problems, RowProblem{
					Source: kind, Line: line, Column: field, Value: raw,
					Reason: fmt.Sprintf("conflicts with %s already read for order %s; the first value was kept", field, id),
				})
			}
		}
		set("gross value", &p.o.GrossValue, &p.setGross)
		set("restaurant discount", &p.o.RestaurantDiscount, &p.setRest)
		set("aggregator discount", &p.o.AggregatorDiscount, &p.setAgg)
		set("packaging charge", &p.o.PackagingCharge, &p.setPack)

		if hasItems {
			name := h.cell(rec, "item name")
			if name == "" {
				continue
			}
			qty, err := ParseCount(h.cell(rec, "quantity"))
			if err != nil {
				out.Problems = append(out.Problems, RowProblem{
					Source: kind, Line: line, Column: "quantity", Value: h.cell(rec, "quantity"),
					Reason: "not a whole quantity; the item line was skipped",
				})
				continue
			}
			if qty == 0 {
				continue
			}
			price := money(line, "unit price", h.cell(rec, "unit price"))
			p.o.Items = append(p.o.Items, Item{Name: name, Qty: qty, UnitPrice: price})
		}
	}

	for _, id := range ids {
		p := byID[id]
		if !p.setGross {
			// No gross column, or an empty one: derive it from the item lines.
			var sum int64
			for _, it := range p.o.Items {
				sum += it.LineValue()
			}
			p.o.GrossValue = sum
			if sum == 0 {
				out.Problems = append(out.Problems, RowProblem{
					Source: kind, Line: p.o.Line, Column: "gross value", Value: id,
					Reason: "order has no gross value and no priced items; it will reconcile against zero",
				})
			}
		}
		out.Orders = append(out.Orders, p.o)
	}
	if len(out.Orders) == 0 {
		return out, errors.New("the order file had a readable header but no usable rows")
	}
	return out, nil
}

// SettlementImport is the result of reading an aggregator settlement export.
type SettlementImport struct {
	Settlements []Settlement `json:"settlements"`
	Problems    []RowProblem `json:"problems"`
	Headers     []string     `json:"headers"`
	Recognised  []string     `json:"recognised"`
	RowsRead    int          `json:"rowsRead"`
	// TaxFromParts is true when the withholding was summed from separate TCS
	// and TDS columns rather than read from one combined column.
	TaxFromParts bool `json:"taxFromParts"`
	// NetDerived is true when the statement had no net settled column and the
	// net was derived as gross less the deductions on the row.
	NetDerived bool `json:"netDerived"`
}

// ParseSettlements reads an aggregator settlement statement.
//
// Deductions are recorded as positive numbers whatever sign the statement
// used, because statements are inconsistent about it and a deduction is a
// deduction. Where the statement splits withholding into TCS and TDS columns
// they are summed; where it also carries a combined column, the combined
// column wins and the parts are ignored, so nothing is counted twice.
func ParseSettlements(r io.Reader) (SettlementImport, error) {
	const kind = "settlements"
	out := SettlementImport{}
	cr := newReader(r)
	recs, probs := readAll(cr, kind)
	out.Problems = append(out.Problems, probs...)
	if len(recs) == 0 {
		return out, errors.New("the settlement file is empty")
	}
	h := buildHeaderIndex(recs[0], settlementColumns, kind)
	out.Headers = recs[0]
	out.Recognised = h.knownFields()
	if !h.has("order id") {
		return out, fmt.Errorf("the settlement file has no order reference column; its headers are: %s",
			strings.Join(out.Headers, ", "))
	}
	if !h.has("net settled") {
		return out, fmt.Errorf("the settlement file has no net settled or payout column; its headers are: %s",
			strings.Join(out.Headers, ", "))
	}
	combined := h.has("tax withheld")
	out.TaxFromParts = !combined && (h.has("tcs") || h.has("tds"))

	abs := func(v int64) int64 {
		if v < 0 {
			return -v
		}
		return v
	}
	money := func(line int, field, raw string) (int64, bool) {
		if raw == "" {
			return 0, true
		}
		v, err := ParseAmount(raw)
		if err != nil {
			out.Problems = append(out.Problems, RowProblem{
				Source: kind, Line: line, Column: field, Value: raw,
				Reason: "not a readable amount; treated as zero",
			})
			return 0, false
		}
		return v, true
	}

	for n, rec := range recs[1:] {
		line := n + 2
		if blank(rec) {
			continue
		}
		out.RowsRead++
		id := NormaliseOrderID(h.cell(rec, "order id"))
		if id == "" {
			out.Problems = append(out.Problems, RowProblem{
				Source: kind, Line: line, Column: "order id",
				Reason: "row has no order reference and was skipped",
			})
			continue
		}
		s := Settlement{OrderID: id, Line: line}
		v, _ := money(line, "commission", h.cell(rec, "commission"))
		s.CommissionCharged = abs(v)
		v, _ = money(line, "pg fee", h.cell(rec, "pg fee"))
		s.PGFee = abs(v)
		if combined {
			v, _ = money(line, "tax withheld", h.cell(rec, "tax withheld"))
			s.TaxWithheld = abs(v)
		} else {
			a, _ := money(line, "tcs", h.cell(rec, "tcs"))
			b, _ := money(line, "tds", h.cell(rec, "tds"))
			s.TaxWithheld = abs(a) + abs(b)
		}
		// The net is signed as the statement signs it: a negative net payout
		// is a real thing (a recovery) and must not be flipped.
		s.NetSettled, _ = money(line, "net settled", h.cell(rec, "net settled"))
		out.Settlements = append(out.Settlements, s)
	}
	if len(out.Settlements) == 0 {
		return out, errors.New("the settlement file had a readable header but no usable rows")
	}
	return out, nil
}

// ParseCosts reads an optional two-column food-cost file: dish name and the
// cost of one unit. It is forgiving about the header and ignores extra
// columns.
func ParseCosts(r io.Reader) (map[string]int64, []RowProblem, error) {
	const kind = "costs"
	cr := newReader(r)
	recs, probs := readAll(cr, kind)
	if len(recs) == 0 {
		return nil, probs, errors.New("the food cost file is empty")
	}
	start := 0
	// Skip a header row if the second cell is not an amount.
	if len(recs[0]) > 1 {
		if _, err := ParseAmount(recs[0][1]); err != nil {
			start = 1
		}
	}
	costs := map[string]int64{}
	for i := start; i < len(recs); i++ {
		rec := recs[i]
		line := i + 1
		if blank(rec) || len(rec) < 2 {
			continue
		}
		name := strings.TrimSpace(rec[0])
		if name == "" {
			continue
		}
		v, err := ParseAmount(rec[1])
		if err != nil {
			probs = append(probs, RowProblem{
				Source: kind, Line: line, Column: "food cost", Value: rec[1],
				Reason: "not a readable amount; the dish was skipped",
			})
			continue
		}
		costs[DishKey(name)] = v
	}
	return costs, probs, nil
}
