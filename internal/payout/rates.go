package payout

// ---------------------------------------------------------------------------
// Statutory withholding rates. This is the ONE place in the tool where a rate
// set by law is written down. Everything else refers to these names, and every
// one of them can be overridden by the user, because notified rates change.
//
// All of these are India-specific. See RatesPhilippines for why there is no
// equivalent set of Philippine numbers here.
// ---------------------------------------------------------------------------

const (
	// GSTTCSSection52Ceiling is the ceiling the statute itself sets: section
	// 52(1) of the Central Goods and Services Tax Act, 2017 allows an
	// e-commerce operator to be required to collect tax at source at a rate
	// "not exceeding one per cent" of the net value of taxable supplies made
	// through it. 1.00% was also the notified combined rate (0.5% CGST +
	// 0.5% SGST/UTGST, or 1% IGST) from 1 October 2018 until 9 July 2024.
	GSTTCSSection52Ceiling BasisPoints = 100 // 1.00%

	// GSTTCSSection52Notified is the combined rate notified with effect from
	// 10 July 2024 — 0.25% CGST + 0.25% SGST/UTGST for an intra-state supply,
	// or 0.5% IGST for an inter-state one. Confirm the rate in force for your
	// settlement period before relying on it.
	GSTTCSSection52Notified BasisPoints = 50 // 0.50%

	// TDSSection194O is the rate at which an e-commerce operator deducts
	// income tax from the gross amount of sales it facilitates for a
	// participant, under section 194-O of the Income-tax Act, 1961, with
	// effect from 1 October 2024. It applies only where the participant has
	// furnished a PAN; without one, section 206AA imposes a higher rate.
	TDSSection194O BasisPoints = 10 // 0.10%

	// TDSSection194OUntilSep2024 is the section 194-O rate that applied up to
	// 30 September 2024, kept so that an older settlement period reconciles.
	TDSSection194OUntilSep2024 BasisPoints = 100 // 1.00%
)

const (
	// DefaultTolerancePaise is how far a settlement may differ from the
	// expected payout and still count as paid correctly: Rs 1.00. Aggregator
	// statements round in several places, so a rupee of noise per order is
	// not a dispute.
	DefaultTolerancePaise int64 = 100

	// DefaultCommissionTolerance is how far the effective commission rate may
	// exceed the agreed rate before it is called an overcharge: 0.01
	// percentage points, which is pure rounding.
	DefaultCommissionTolerance BasisPoints = 1
)

// RateSet is every rate and contract option needed to turn one order in the
// books into the payout that should have landed in the bank.
//
// Zero is a legitimate value for any rate. A rate set with both withholding
// rates at zero simply means "do not expect anything to be withheld", which
// is the correct setting in several real cases documented below.
type RateSet struct {
	// Name is shown in the UI and written into exports so that a dispute
	// letter says which assumptions produced the numbers.
	Name string

	// GSTTCS is tax collected at source by the operator, section 52 CGST.
	GSTTCS BasisPoints

	// TDS194O is income tax deducted by the operator, section 194-O.
	TDS194O BasisPoints

	// PGFee is the agreed payment-gateway or payment-mechanism fee, as a
	// share of the amount the customer actually paid. Leave it at zero if
	// your contract has no such fee, or if you would rather accept whatever
	// the operator charged than audit it (see Options.AuditPGFee).
	PGFee BasisPoints

	// PackagingBorneByRestaurant records who pays for packaging. When true,
	// the packaging charge on the order is a cost the restaurant carries and
	// is deducted from the expected payout. When false the operator collects
	// it from the customer and keeps it, so it neither adds to nor subtracts
	// from the restaurant's payout.
	PackagingBorneByRestaurant bool
}

// RatesIndia is the documented India default: GST TCS at the section 52
// ceiling of 1.00% and TDS at the section 194-O rate of 0.10%.
//
// Read the next function before using this for a pure restaurant business.
func RatesIndia() RateSet {
	return RateSet{
		Name:                       "India (TCS 1.00% + TDS 0.10%)",
		GSTTCS:                     GSTTCSSection52Ceiling,
		TDS194O:                    TDSSection194O,
		PackagingBorneByRestaurant: true,
	}
}

// RatesIndiaRestaurant is the set that fits most restaurants and cloud
// kitchens selling cooked food through a delivery platform.
//
// Restaurant service supplied through an e-commerce operator was notified
// under section 9(5) of the CGST Act by Notification 17/2021-Central Tax
// (Rate) with effect from 1 January 2022. The operator pays the GST on that
// supply itself, and CBIC Circular 167/23/2021-GST states that the operator is
// therefore not required to collect tax at source under section 52 on it. So
// for cooked-food orders the expected GST TCS is nil — but TDS under section
// 194-O still applies, and TCS does still apply to anything you sell through
// the platform that is not covered by section 9(5), such as goods, or
// restaurant service at "specified premises".
//
// If your settlement statement does show a TCS line against cooked food, that
// is itself worth asking the operator about.
func RatesIndiaRestaurant() RateSet {
	return RateSet{
		Name:                       "India restaurant service, s.9(5) (TCS nil + TDS 0.10%)",
		GSTTCS:                     0,
		TDS194O:                    TDSSection194O,
		PackagingBorneByRestaurant: true,
	}
}

// RatesPhilippines deliberately withholds nothing.
//
// The Philippine withholding regime for online platform operators is not the
// Indian one and this tool does not state a Philippine rate it cannot source.
// Reconciliation still works in full: commission, payment-gateway fee,
// packaging and discounts are all audited, and the tax line is simply expected
// to be nil. If your platform does withhold under a BIR issuance that applies
// to you, enter that rate yourself and the whole calculation follows.
func RatesPhilippines() RateSet {
	return RateSet{
		Name:                       "Philippines (no withholding assumed — enter your own rate)",
		GSTTCS:                     0,
		TDS194O:                    0,
		PackagingBorneByRestaurant: true,
	}
}

// RatesNone switches withholding off entirely, for a platform that settles
// gross and leaves every tax to the restaurant.
func RatesNone() RateSet {
	return RateSet{Name: "No withholding", PackagingBorneByRestaurant: true}
}

// Presets lists the rate sets the UI offers, in the order it offers them.
func Presets() []RateSet {
	return []RateSet{
		RatesIndiaRestaurant(),
		RatesIndia(),
		RatesPhilippines(),
		RatesNone(),
	}
}

// Options parameterise one reconciliation run.
type Options struct {
	// Aggregator is the platform these statements came from, written into
	// exports so a dispute names the right counterparty.
	Aggregator string

	// AgreedCommissionPct is the commission rate in the restaurant's
	// agreement with the platform, in basis points.
	AgreedCommissionPct BasisPoints

	// Rates holds the withholding and fee rates. See RateSet.
	Rates RateSet

	// TolerancePaise is the per-order rounding allowance. Zero means use
	// DefaultTolerancePaise.
	TolerancePaise int64

	// CommissionTolerance is the allowance on the effective commission rate.
	// Zero means use DefaultCommissionTolerance.
	CommissionTolerance BasisPoints

	// AuditPGFee decides whether the payment-gateway fee is checked against
	// Rates.PGFee (true) or simply accepted as charged (false). It is false
	// by default so that a restaurant with no agreed gateway rate does not
	// see every single order reported as short-paid.
	AuditPGFee bool
}

// Tolerance returns the per-order rounding allowance actually in force.
func (o Options) Tolerance() int64 {
	if o.TolerancePaise <= 0 {
		return DefaultTolerancePaise
	}
	return o.TolerancePaise
}

// CommissionTol returns the commission-rate allowance actually in force.
func (o Options) CommissionTol() BasisPoints {
	if o.CommissionTolerance <= 0 {
		return DefaultCommissionTolerance
	}
	return o.CommissionTolerance
}
