package payout

import "fmt"

// Expected is the payout one order should have produced, broken into the lines
// a dispute needs to quote. Every amount is in paise; every deduction is a
// positive number that was subtracted.
type Expected struct {
	OrderID string `json:"orderId"`
	// Base is the commissionable base. See CommissionableBase.
	Base               int64       `json:"base"`
	GrossValue         int64       `json:"grossValue"`
	RestaurantDiscount int64       `json:"restaurantDiscount"`
	AggregatorDiscount int64       `json:"aggregatorDiscount"`
	CommissionPct      BasisPoints `json:"commissionPct"`
	Commission         int64       `json:"commission"`
	PGFee              int64       `json:"pgFee"`
	Packaging          int64       `json:"packaging"`
	GSTTCS             int64       `json:"gstTcs"`
	TDS194O            int64       `json:"tds194o"`
	TaxWithheld        int64       `json:"taxWithheld"`
	// Payout is the amount that should have reached the bank for this order.
	Payout int64 `json:"payout"`
}

// ExpectedPayout works out what one order should have paid the restaurant:
//
//	payout = GrossValue
//	       - RestaurantDiscount          (funded by the restaurant, never earned)
//	       - Commission                  (agreedCommissionPct x commissionable base)
//	       - PGFee                       (rates.PGFee x amount the customer paid)
//	       - Packaging                   (only if borne by the restaurant)
//	       - TaxWithheld                 (GST TCS + section 194-O TDS on the base)
//
// The commissionable base is CommissionableBase and is defined in exactly one
// place; the aggregator-funded discount is deliberately not deducted anywhere
// in this calculation, because the restaurant sold at full price.
func ExpectedPayout(o Order, agreedCommissionPct BasisPoints, rates RateSet) Expected {
	base := CommissionableBase(o)
	wbase := WithholdingBase(o)

	e := Expected{
		OrderID:            o.ID,
		Base:               base,
		GrossValue:         o.GrossValue,
		RestaurantDiscount: o.RestaurantDiscount,
		AggregatorDiscount: o.AggregatorDiscount,
		CommissionPct:      agreedCommissionPct,
		Commission:         ApplyRate(base, agreedCommissionPct),
		PGFee:              ApplyRate(CustomerPaid(o), rates.PGFee),
		GSTTCS:             ApplyRate(wbase, rates.GSTTCS),
		TDS194O:            ApplyRate(wbase, rates.TDS194O),
	}
	if rates.PackagingBorneByRestaurant {
		e.Packaging = o.PackagingCharge
	}
	e.TaxWithheld = e.GSTTCS + e.TDS194O
	e.Payout = o.GrossValue - o.RestaurantDiscount - e.Commission - e.PGFee - e.Packaging - e.TaxWithheld
	return e
}

// WithPGFee returns a copy of the expectation in which the payment-gateway fee
// is the one the statement actually charged, with the payout adjusted to
// match. The reconciler uses it when Options.AuditPGFee is false, so that a
// restaurant with no agreed gateway rate is not told every order is short.
func (e Expected) WithPGFee(charged int64) Expected {
	e.Payout += e.PGFee - charged
	e.PGFee = charged
	return e
}

// Class is how one order's variance is classified.
type Class string

const (
	// ClassOK means settled within tolerance of the expected payout.
	ClassOK Class = "OK"
	// ClassShortPaid means less money arrived than was owed.
	ClassShortPaid Class = "ShortPaid"
	// ClassOverPaid means more money arrived than was owed. It is not a
	// windfall: aggregators recover overpayments from later settlements.
	ClassOverPaid Class = "OverPaid"
)

// Variance compares what landed with what was owed. The result is
// netSettled - expected, so a negative variance is a short payment, and the
// class is decided by the tolerance: anything whose absolute value is within
// tolerance counts as correctly paid.
func Variance(netSettled, expected, tolerancePaise int64) (int64, Class) {
	if tolerancePaise < 0 {
		tolerancePaise = -tolerancePaise
	}
	delta := netSettled - expected
	switch {
	case delta >= -tolerancePaise && delta <= tolerancePaise:
		return delta, ClassOK
	case delta < 0:
		return delta, ClassShortPaid
	default:
		return delta, ClassOverPaid
	}
}

// Overcharge reports whether the commission actually charged on one order
// implies a higher rate than the agreement allows.
type Overcharge struct {
	OrderID string `json:"orderId"`
	// Base is the commissionable base the effective rate was measured against.
	Base      int64       `json:"base"`
	Charged   int64       `json:"charged"`
	AgreedPct BasisPoints `json:"agreedPct"`
	// EffectivePct is Charged as a rate of Base. Valid only when Measurable.
	EffectivePct BasisPoints `json:"effectivePct"`
	// Measurable is false when the base is zero or negative — a fully
	// discounted or cancelled order has no rate to measure.
	Measurable bool `json:"measurable"`
	// Agreed is the commission the agreed rate would have produced.
	Agreed int64 `json:"agreed"`
	// Excess is Charged - Agreed in paise: the money impact, which is what a
	// dispute letter actually claims.
	Excess int64 `json:"excess"`
	// Flagged is true only when the effective rate exceeds the agreed rate by
	// more than the tolerance.
	Flagged bool `json:"flagged"`
}

// CommissionOvercharge checks the commission on one order against the agreed
// rate. An order charged exactly at the agreed rate is never flagged; the
// tolerance exists only to absorb the operator's own rounding, so pass
// DefaultCommissionTolerance unless you have a reason not to.
func CommissionOvercharge(o Order, charged int64, agreedPct, tol BasisPoints) Overcharge {
	if tol < 0 {
		tol = -tol
	}
	base := CommissionableBase(o)
	oc := Overcharge{
		OrderID:   o.ID,
		Base:      base,
		Charged:   charged,
		AgreedPct: agreedPct,
		Agreed:    ApplyRate(base, agreedPct),
	}
	oc.Excess = charged - oc.Agreed
	eff, ok := EffectiveRate(charged, base)
	oc.EffectivePct, oc.Measurable = eff, ok
	// An unmeasurable rate is never flagged as a rate breach. If money was
	// nonetheless charged on a zero base, the order's variance will show it.
	oc.Flagged = ok && eff > agreedPct+tol
	return oc
}

// Reason is the one-line explanation of an overcharge, as it appears in the
// dispute export.
func (oc Overcharge) Reason() string {
	if !oc.Flagged {
		return ""
	}
	return fmt.Sprintf("Commission charged at %s of %s against agreed %s — excess %s",
		oc.EffectivePct, Money(oc.Base), oc.AgreedPct, Money(oc.Excess))
}
