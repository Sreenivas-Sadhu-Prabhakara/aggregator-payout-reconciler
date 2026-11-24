package payout

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// Export names the downloadable views of a report.
type Export string

const (
	ExportShortPaid         Export = "short-paid"
	ExportOverPaid          Export = "over-paid"
	ExportOK                Export = "reconciled"
	ExportOvercharge        Export = "commission-overcharge"
	ExportInBooksNotSettled Export = "in-books-not-settled"
	ExportSettledNotInBooks Export = "settled-not-in-books"
	ExportDishes            Export = "dish-margins"
	ExportDisputes          Export = "dispute-list"
	ExportProblems          Export = "unreadable-rows"
)

// Exports lists every export, in the order the UI offers them.
func Exports() []Export {
	return []Export{
		ExportDisputes, ExportShortPaid, ExportOverPaid, ExportOK,
		ExportOvercharge, ExportInBooksNotSettled, ExportSettledNotInBooks,
		ExportDishes, ExportProblems,
	}
}

// Valid reports whether e is a known export.
func (e Export) Valid() bool {
	for _, k := range Exports() {
		if k == e {
			return true
		}
	}
	return false
}

// Title is the human name of the export.
func (e Export) Title() string {
	switch e {
	case ExportShortPaid:
		return "Short-paid orders"
	case ExportOverPaid:
		return "Over-paid orders"
	case ExportOK:
		return "Reconciled orders"
	case ExportOvercharge:
		return "Commission overcharges"
	case ExportInBooksNotSettled:
		return "In books, never settled"
	case ExportSettledNotInBooks:
		return "Settled, not in books"
	case ExportDishes:
		return "Per-dish margins"
	case ExportDisputes:
		return "Dispute list"
	case ExportProblems:
		return "Unreadable rows"
	}
	return string(e)
}

// WriteExport writes one export of the report as CSV.
//
// Amounts are written as plain decimals so a spreadsheet reads them as
// numbers. Nothing in this package transmits anything: an export is a file for
// the owner to check and send themselves.
func WriteExport(w io.Writer, rep Report, e Export) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	var err error
	switch e {
	case ExportShortPaid:
		err = writeRows(cw, rep.ShortPaid())
	case ExportOverPaid:
		err = writeRows(cw, rep.OverPaid())
	case ExportOK:
		err = writeRows(cw, rep.OK())
	case ExportOvercharge:
		err = writeOvercharges(cw, rep.Overcharges(), rep.Options)
	case ExportInBooksNotSettled:
		err = writeMissing(cw, rep.InBooksNotSettled)
	case ExportSettledNotInBooks:
		err = writeExtra(cw, rep.SettledNotInBooks)
	case ExportDishes:
		err = writeDishes(cw, rep.Dishes)
	case ExportDisputes:
		err = writeDisputes(cw, rep)
	case ExportProblems:
		err = writeProblems(cw, rep.Problems)
	default:
		return fmt.Errorf("unknown export %q", string(e))
	}
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

var rowHeader = []string{
	"Order ID", "Date", "Gross value", "Restaurant discount", "Aggregator discount",
	"Commissionable base", "Agreed commission %", "Expected commission", "Commission charged",
	"Effective commission %", "Expected PG fee", "PG fee charged", "Packaging borne",
	"Expected tax withheld", "Tax withheld", "Expected payout", "Net settled",
	"Variance", "Category", "Reason",
}

func rowRecord(r Row) []string {
	eff := ""
	if r.Overcharge.Measurable {
		eff = r.Overcharge.EffectivePct.String()
	}
	return []string{
		r.OrderID, r.Date,
		Decimal(r.Expected.GrossValue), Decimal(r.Expected.RestaurantDiscount), Decimal(r.Expected.AggregatorDiscount),
		Decimal(r.Expected.Base), r.Expected.CommissionPct.String(),
		Decimal(r.Overcharge.Agreed), Decimal(r.Settlement.CommissionCharged), eff,
		Decimal(r.Expected.PGFee), Decimal(r.Settlement.PGFee), Decimal(r.Expected.Packaging),
		Decimal(r.Expected.TaxWithheld), Decimal(r.Settlement.TaxWithheld),
		Decimal(r.Expected.Payout), Decimal(r.Settlement.NetSettled),
		Decimal(r.Variance), string(r.Class), r.Reason,
	}
}

func writeRows(cw *csv.Writer, rows []Row) error {
	if err := cw.Write(rowHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(rowRecord(r)); err != nil {
			return err
		}
	}
	return nil
}

func writeOvercharges(cw *csv.Writer, rows []Row, opts Options) error {
	if err := cw.Write([]string{
		"Order ID", "Date", "Commissionable base", "Agreed commission %",
		"Commission at agreed rate", "Commission charged", "Effective commission %",
		"Excess charged", "Reason",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.OrderID, r.Date, Decimal(r.Overcharge.Base), opts.AgreedCommissionPct.String(),
			Decimal(r.Overcharge.Agreed), Decimal(r.Overcharge.Charged),
			r.Overcharge.EffectivePct.String(), Decimal(r.Overcharge.Excess), r.Overcharge.Reason(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeMissing(cw *csv.Writer, ms []Missing) error {
	if err := cw.Write([]string{
		"Order ID", "Date", "Gross value", "Restaurant discount",
		"Commissionable base", "Expected payout", "Reason",
	}); err != nil {
		return err
	}
	for _, m := range ms {
		if err := cw.Write([]string{
			m.Order.ID, m.Order.Date, Decimal(m.Order.GrossValue), Decimal(m.Order.RestaurantDiscount),
			Decimal(m.Expected.Base), Decimal(m.Expected.Payout),
			"Order is in the books but no settlement line was found for it",
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeExtra(cw *csv.Writer, es []Extra) error {
	if err := cw.Write([]string{
		"Order ID", "Commission charged", "PG fee", "Tax withheld", "Net settled", "Statement rows", "Reason",
	}); err != nil {
		return err
	}
	for _, x := range es {
		if err := cw.Write([]string{
			x.Settlement.OrderID, Decimal(x.Settlement.CommissionCharged), Decimal(x.Settlement.PGFee),
			Decimal(x.Settlement.TaxWithheld), Decimal(x.Settlement.NetSettled),
			strconv.Itoa(x.Settlement.Lines),
			"Settlement line has no matching order in the books",
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeDishes(cw *csv.Writer, ds []DishRoll) error {
	if err := cw.Write([]string{
		"Dish", "Units sold", "Menu price", "Food cost", "Food cost known",
		"Commission", "Packaging", "Tax withheld", "Net realisation",
		"Gross margin", "Gross margin %", "Net margin", "Net margin %",
		"Total net margin", "Verdict",
	}); err != nil {
		return err
	}
	for _, d := range ds {
		verdict := "Profitable after commission"
		switch {
		case d.FlattersToDeceive:
			verdict = "Looks profitable on the menu, loses money on the platform"
		case d.LossMaking:
			verdict = "Loss-making"
		}
		if !d.CostKnown {
			verdict = "No food cost entered — margins are an upper bound"
		}
		known := "no"
		if d.CostKnown {
			known = "yes"
		}
		if err := cw.Write([]string{
			d.Name, strconv.Itoa(d.Units), Decimal(d.MenuPrice), Decimal(d.FoodCost), known,
			Decimal(d.Commission), Decimal(d.Packaging), Decimal(d.TaxWithheld), Decimal(d.NetRealisation),
			Decimal(d.GrossMargin), d.GrossMarginPct.String(), Decimal(d.NetMargin), d.NetMarginPct.String(),
			Decimal(d.TotalNetMargin), verdict,
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeDisputes writes the list the owner sends to the platform: one line per
// order where money is missing, with the arithmetic shown so the claim can be
// checked by the person reading it.
func writeDisputes(cw *csv.Writer, rep Report) error {
	if err := cw.Write([]string{
		"Order ID", "Date", "Issue", "Gross value", "Restaurant discount",
		"Commissionable base", "Agreed commission %", "Commission at agreed rate",
		"Commission charged", "Effective commission %", "Expected payout",
		"Net settled", "Amount claimed", "Basis of claim",
	}); err != nil {
		return err
	}
	for _, r := range rep.ShortPaid() {
		issue := "Short payment"
		if r.Overcharge.Flagged {
			issue = "Short payment and commission overcharge"
		}
		eff := ""
		if r.Overcharge.Measurable {
			eff = r.Overcharge.EffectivePct.String()
		}
		if err := cw.Write([]string{
			r.OrderID, r.Date, issue,
			Decimal(r.Expected.GrossValue), Decimal(r.Expected.RestaurantDiscount),
			Decimal(r.Expected.Base), r.Expected.CommissionPct.String(),
			Decimal(r.Overcharge.Agreed), Decimal(r.Settlement.CommissionCharged), eff,
			Decimal(r.Expected.Payout), Decimal(r.Settlement.NetSettled),
			Decimal(-r.Variance), r.Reason,
		}); err != nil {
			return err
		}
	}
	for _, m := range rep.InBooksNotSettled {
		if err := cw.Write([]string{
			m.Order.ID, m.Order.Date, "No settlement received",
			Decimal(m.Order.GrossValue), Decimal(m.Order.RestaurantDiscount),
			Decimal(m.Expected.Base), m.Expected.CommissionPct.String(),
			Decimal(m.Expected.Commission), "", "",
			Decimal(m.Expected.Payout), "0.00", Decimal(m.Expected.Payout),
			"Order is in the books but no settlement line was found for it",
		}); err != nil {
			return err
		}
	}
	// A trailing total line, because the first thing the other side asks is
	// what the whole claim comes to.
	return cw.Write([]string{
		"TOTAL", "", "Amount claimed across the rows above", "", "", "", "", "", "", "", "", "",
		Decimal(rep.Totals.MoneyOwedBack),
		fmt.Sprintf("Short payments %s plus unsettled orders %s. Excess commission of %s is already included in the short payments and is not claimed twice.",
			Money(rep.Totals.TotalShortPaid), Money(rep.Totals.InBooksNotSettledValue),
			Money(rep.Totals.TotalCommissionOvercharge)),
	})
}

func writeProblems(cw *csv.Writer, ps []RowProblem) error {
	if err := cw.Write([]string{"File", "Line", "Column", "Value", "Problem"}); err != nil {
		return err
	}
	for _, p := range ps {
		if err := cw.Write([]string{p.Source, strconv.Itoa(p.Line), p.Column, p.Value, p.Reason}); err != nil {
			return err
		}
	}
	return nil
}
