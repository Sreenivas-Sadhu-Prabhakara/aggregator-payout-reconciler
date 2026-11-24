package payout

import (
	"fmt"
	"sort"
)

// Row is one reconciled order: what the books said, what the statement said,
// and the gap between them.
type Row struct {
	OrderID    string     `json:"orderId"`
	Date       string     `json:"date"`
	Expected   Expected   `json:"expected"`
	Settlement Settlement `json:"settlement"`
	// Variance is NetSettled - Expected.Payout. Negative means short-paid.
	Variance   int64      `json:"variance"`
	Class      Class      `json:"class"`
	Overcharge Overcharge `json:"overcharge"`
	// Reason is the human-readable explanation used in the dispute export.
	Reason string `json:"reason"`
}

// Missing is an order in the books for which no settlement line was found:
// money that never arrived at all, which is worse than being short-paid.
type Missing struct {
	Order    Order    `json:"order"`
	Expected Expected `json:"expected"`
}

// Extra is a settlement line with no matching order in the books. It is not
// money owed — it usually means the books are incomplete, or the statement
// covers an order the restaurant recorded under a different reference.
type Extra struct {
	Settlement Settlement `json:"settlement"`
}

// Totals are the figures the summary shows.
type Totals struct {
	Orders      int `json:"orders"`
	Settlements int `json:"settlements"`
	Matched     int `json:"matched"`

	OKCount        int `json:"okCount"`
	ShortPaidCount int `json:"shortPaidCount"`
	OverPaidCount  int `json:"overPaidCount"`

	// TotalShortPaid is the sum of the shortfalls, as a positive number.
	TotalShortPaid int64 `json:"totalShortPaid"`
	// TotalOverPaid is the sum of the overpayments, as a positive number.
	TotalOverPaid int64 `json:"totalOverPaid"`
	// NetVariance is TotalOverPaid - TotalShortPaid across matched orders.
	NetVariance int64 `json:"netVariance"`

	// OverchargeCount and TotalCommissionOvercharge explain how much of the
	// shortfall is excess commission. They are NOT added to MoneyOwedBack:
	// the expected payout is computed at the agreed commission rate, so every
	// rupee of excess commission is already inside TotalShortPaid. Adding it
	// again would overstate the claim, and an overstated claim is the fastest
	// way to lose a real one.
	OverchargeCount           int   `json:"overchargeCount"`
	TotalCommissionOvercharge int64 `json:"totalCommissionOvercharge"`

	InBooksNotSettledCount int   `json:"inBooksNotSettledCount"`
	InBooksNotSettledValue int64 `json:"inBooksNotSettledValue"`
	SettledNotInBooksCount int   `json:"settledNotInBooksCount"`
	SettledNotInBooksValue int64 `json:"settledNotInBooksValue"`

	// MoneyOwedBack is the headline figure: the shortfall on orders that were
	// settled, plus the whole expected payout of orders that were never
	// settled at all.
	MoneyOwedBack int64 `json:"moneyOwedBack"`

	// MultiLineOrders counts orders whose settlement arrived over more than
	// one statement row, which this tool sums into a single settlement.
	MultiLineOrders int `json:"multiLineOrders"`
	// DuplicateOrderIDs counts order references that appeared more than once
	// in the books; only the first is reconciled and the rest are reported.
	DuplicateOrderIDs int `json:"duplicateOrderIds"`
}

// Report is the whole result of one reconciliation run.
type Report struct {
	Options           Options    `json:"options"`
	Rows              []Row      `json:"rows"`
	InBooksNotSettled []Missing  `json:"inBooksNotSettled"`
	SettledNotInBooks []Extra    `json:"settledNotInBooks"`
	Dishes            []DishRoll `json:"dishes"`
	Totals            Totals     `json:"totals"`
	// Problems carries anything the matcher could not make sense of, so that
	// nothing is silently dropped.
	Problems []RowProblem `json:"problems"`
}

// ShortPaid returns only the short-paid rows, worst first.
func (r Report) ShortPaid() []Row { return r.byClass(ClassShortPaid) }

// OverPaid returns only the over-paid rows, largest first.
func (r Report) OverPaid() []Row { return r.byClass(ClassOverPaid) }

// OK returns the rows that reconciled within tolerance.
func (r Report) OK() []Row { return r.byClass(ClassOK) }

func (r Report) byClass(c Class) []Row {
	out := make([]Row, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Class == c {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c == ClassOverPaid {
			return out[i].Variance > out[j].Variance
		}
		return out[i].Variance < out[j].Variance
	})
	return out
}

// Overcharges returns the flagged commission overcharges, largest first.
func (r Report) Overcharges() []Row {
	out := make([]Row, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Overcharge.Flagged {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Overcharge.Excess > out[j].Overcharge.Excess
	})
	return out
}

// Reconcile matches the order book against the settlement statement and
// classifies every order.
//
// Matching is on the normalised order reference (see NormaliseOrderID). Where
// a statement settles one order over several rows, those rows are summed into
// one settlement, because that is what the bank received. Where the books
// record the same reference twice, the first is reconciled and the duplicate
// is reported rather than silently merged — two orders sharing a reference is
// a bookkeeping fault the owner needs to see.
func Reconcile(orders []Order, settlements []Settlement, costs map[string]int64, opts Options) Report {
	rep := Report{Options: opts}
	tol := opts.Tolerance()
	ctol := opts.CommissionTol()

	// Fold the statement into one settlement per order reference.
	settled := make(map[string]*Settlement, len(settlements))
	order2 := make([]string, 0, len(settlements))
	for _, s := range settlements {
		key := NormaliseOrderID(s.OrderID)
		if key == "" {
			rep.Problems = append(rep.Problems, RowProblem{
				Source: "settlements", Line: s.Line, Column: "order id",
				Reason: "settlement row has no order reference",
			})
			continue
		}
		if prev, ok := settled[key]; ok {
			prev.CommissionCharged += s.CommissionCharged
			prev.PGFee += s.PGFee
			prev.TaxWithheld += s.TaxWithheld
			prev.NetSettled += s.NetSettled
			prev.Lines++
			continue
		}
		cp := s
		cp.OrderID = key
		cp.Lines = 1
		settled[key] = &cp
		order2 = append(order2, key)
	}
	for _, k := range order2 {
		if settled[k].Lines > 1 {
			rep.Totals.MultiLineOrders++
		}
	}

	seen := make(map[string]bool, len(orders))
	used := make(map[string]bool, len(settled))

	for _, o := range orders {
		key := NormaliseOrderID(o.ID)
		if key == "" {
			rep.Problems = append(rep.Problems, RowProblem{
				Source: "orders", Line: o.Line, Column: "order id",
				Reason: "order row has no order reference",
			})
			continue
		}
		if seen[key] {
			rep.Totals.DuplicateOrderIDs++
			rep.Problems = append(rep.Problems, RowProblem{
				Source: "orders", Line: o.Line, Column: "order id", Value: o.ID,
				Reason: "order reference appears more than once in the books; only the first row is reconciled",
			})
			continue
		}
		seen[key] = true
		o.ID = key
		rep.Totals.Orders++

		exp := ExpectedPayout(o, opts.AgreedCommissionPct, opts.Rates)
		s, ok := settled[key]
		if !ok {
			rep.InBooksNotSettled = append(rep.InBooksNotSettled, Missing{Order: o, Expected: exp})
			rep.Totals.InBooksNotSettledCount++
			rep.Totals.InBooksNotSettledValue += exp.Payout
			continue
		}
		used[key] = true

		if !opts.AuditPGFee {
			exp = exp.WithPGFee(s.PGFee)
		}
		v, cls := Variance(s.NetSettled, exp.Payout, tol)
		row := Row{
			OrderID:    key,
			Date:       o.Date,
			Expected:   exp,
			Settlement: *s,
			Variance:   v,
			Class:      cls,
			Overcharge: CommissionOvercharge(o, s.CommissionCharged, opts.AgreedCommissionPct, ctol),
		}
		row.Reason = explain(row)
		rep.Rows = append(rep.Rows, row)

		rep.Totals.Matched++
		switch cls {
		case ClassOK:
			rep.Totals.OKCount++
		case ClassShortPaid:
			rep.Totals.ShortPaidCount++
			rep.Totals.TotalShortPaid += -v
		case ClassOverPaid:
			rep.Totals.OverPaidCount++
			rep.Totals.TotalOverPaid += v
		}
		if row.Overcharge.Flagged {
			rep.Totals.OverchargeCount++
			rep.Totals.TotalCommissionOvercharge += row.Overcharge.Excess
		}
	}

	for _, k := range order2 {
		if used[k] {
			continue
		}
		rep.SettledNotInBooks = append(rep.SettledNotInBooks, Extra{Settlement: *settled[k]})
		rep.Totals.SettledNotInBooksCount++
		rep.Totals.SettledNotInBooksValue += settled[k].NetSettled
	}

	rep.Totals.Settlements = len(settled)
	rep.Totals.NetVariance = rep.Totals.TotalOverPaid - rep.Totals.TotalShortPaid
	rep.Totals.MoneyOwedBack = rep.Totals.TotalShortPaid + rep.Totals.InBooksNotSettledValue
	rep.Dishes = WorstDishes(orders, costs, opts)
	return rep
}

// explain writes the one line a dispute list carries for an order.
func explain(r Row) string {
	switch r.Class {
	case ClassShortPaid:
		s := fmt.Sprintf("Short-paid by %s: expected %s, settled %s",
			Money(-r.Variance), Money(r.Expected.Payout), Money(r.Settlement.NetSettled))
		if r.Overcharge.Flagged {
			s += ". " + r.Overcharge.Reason()
		}
		return s
	case ClassOverPaid:
		return fmt.Sprintf("Over-paid by %s: expected %s, settled %s",
			Money(r.Variance), Money(r.Expected.Payout), Money(r.Settlement.NetSettled))
	default:
		if r.Overcharge.Flagged {
			return "Settled within tolerance, but " + r.Overcharge.Reason()
		}
		return "Settled within tolerance"
	}
}
