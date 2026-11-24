package payout

// Item is one dish line on an order, as the order export lists it.
type Item struct {
	Name string `json:"name"`
	Qty  int    `json:"qty"`
	// UnitPrice is the menu price of one unit, in paise, before any discount.
	UnitPrice int64 `json:"unitPrice"`
}

// LineValue is the menu value of the whole line.
func (it Item) LineValue() int64 { return it.UnitPrice * int64(it.Qty) }

// Order is one order as the restaurant's own books record it. Every amount is
// in paise.
type Order struct {
	// ID is the normalised order reference. See NormaliseOrderID.
	ID string `json:"id"`
	// Date is the order date, as written in the export, kept verbatim so a
	// dispute quotes exactly what the statement says.
	Date string `json:"date"`
	// GrossValue is the menu value of the food sold, before any discount and
	// excluding the packaging charge.
	GrossValue int64 `json:"grossValue"`
	// RestaurantDiscount is the part of the customer's discount the
	// restaurant funded.
	RestaurantDiscount int64 `json:"restaurantDiscount"`
	// AggregatorDiscount is the part the platform funded out of its own
	// pocket. The restaurant is still owed the full price on it.
	AggregatorDiscount int64 `json:"aggregatorDiscount"`
	// PackagingCharge is the packaging amount on the order.
	PackagingCharge int64 `json:"packagingCharge"`
	// Items are the dish lines, when the export carries them.
	Items []Item `json:"items,omitempty"`
	// Line is the row of the import this order came from, for error messages.
	Line int `json:"line,omitempty"`
}

// Settlement is one order's worth of deductions as the aggregator's settlement
// statement reports them. Every amount is in paise, and every deduction is
// recorded as a positive number however the statement signs it.
type Settlement struct {
	OrderID           string `json:"orderId"`
	CommissionCharged int64  `json:"commissionCharged"`
	PGFee             int64  `json:"pgFee"`
	TaxWithheld       int64  `json:"taxWithheld"`
	NetSettled        int64  `json:"netSettled"`
	// Lines is how many statement rows were summed into this settlement. An
	// aggregator sometimes settles one order in two tranches.
	Lines int `json:"lines,omitempty"`
	// Line is the row of the import this settlement came from.
	Line int `json:"line,omitempty"`
}

// ---------------------------------------------------------------------------
// The commissionable base — defined once, here, and nowhere else.
// ---------------------------------------------------------------------------

// CommissionableBase is the amount the aggregator's commission percentage is
// applied to. This is the single definition used by every rule in the package.
//
//	base = GrossValue - RestaurantDiscount
//
// Three decisions are built into that line, and each one is worth money:
//
//   - The AggregatorDiscount is NOT subtracted. When the platform funds a
//     promotion it is buying the discount for the customer; the restaurant
//     sold at full price and commission belongs on full price. A statement
//     that charges commission on the pre-discount value but pays out on the
//     post-discount value is the single most common way a payout goes wrong.
//   - The RestaurantDiscount IS subtracted. That money was never earned, so
//     no commission is due on it.
//   - The PackagingCharge is excluded. Packaging is a pass-through, not a
//     sale, and is handled separately in ExpectedPayout. Taxes and the
//     delivery fee are excluded for the same reason — they are not the
//     restaurant's sale.
//
// If your agreement defines the base differently, the effective-commission
// figures this tool reports will differ from your contract by exactly that
// difference, so read this definition before quoting a number at the platform.
func CommissionableBase(o Order) int64 {
	base := o.GrossValue - o.RestaurantDiscount
	if base < 0 {
		return 0
	}
	return base
}

// CustomerPaid is what the diner's card or UPI was actually charged for the
// order: the menu value less every discount, plus the packaging charge. The
// payment-gateway fee is a share of this amount, not of the commissionable
// base, because the gateway processed this amount.
func CustomerPaid(o Order) int64 {
	paid := o.GrossValue - o.RestaurantDiscount - o.AggregatorDiscount + o.PackagingCharge
	if paid < 0 {
		return 0
	}
	return paid
}

// WithholdingBase is the amount the two withholding rates are applied to.
//
// It is the commissionable base. The statutes do not define their bases
// identically — section 52 speaks of the net value of taxable supplies and
// section 194-O of the gross amount of sales — and the operator's own
// computation may differ from both. Using one base for both is this tool's
// documented simplification; where it matters, set the rates to zero and
// compare the withholding lines on the statement by hand.
func WithholdingBase(o Order) int64 { return CommissionableBase(o) }
