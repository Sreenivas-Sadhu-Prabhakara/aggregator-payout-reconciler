package payout

import (
	"sort"
	"strings"
)

// DishEconomics is what one unit of a dish really earns on the platform.
// Every amount is per unit, in paise.
type DishEconomics struct {
	Name string `json:"name"`
	// MenuPrice is the listed price of one unit on the platform.
	MenuPrice int64 `json:"menuPrice"`
	// FoodCost is the cost of making one unit: ingredients, and whatever else
	// the kitchen counts in its plate cost.
	FoodCost int64 `json:"foodCost"`
	// Commission, Packaging and TaxWithheld are the deductions per unit.
	Commission  int64 `json:"commission"`
	Packaging   int64 `json:"packaging"`
	TaxWithheld int64 `json:"taxWithheld"`
	// NetRealisation is what reaches the bank for one unit:
	// MenuPrice - Commission - Packaging - TaxWithheld.
	NetRealisation int64 `json:"netRealisation"`
	// GrossMargin is the margin the menu card appears to give:
	// MenuPrice - FoodCost. This is the number that misleads.
	GrossMargin    int64       `json:"grossMargin"`
	GrossMarginPct BasisPoints `json:"grossMarginPct"`
	// NetMargin is the margin that actually exists:
	// NetRealisation - FoodCost.
	NetMargin    int64       `json:"netMargin"`
	NetMarginPct BasisPoints `json:"netMarginPct"`
	// LossMaking is true when NetMargin is below zero: every unit sold on the
	// platform loses money.
	LossMaking bool `json:"lossMaking"`
	// FlattersToDeceive is true when the dish shows a positive gross margin
	// and a negative net one — profitable on the menu card, loss-making on
	// the platform. These are the dishes worth repricing or delisting.
	FlattersToDeceive bool `json:"flattersToDeceive"`
}

// DishMargin works out the true margin on one unit of a dish once the platform
// has taken its share.
//
// commissionPct is applied to the dish's menu price, which is its share of the
// commissionable base when the order carries no discount. packaging is the
// packaging cost attributable to one unit, and taxRate is the combined
// withholding rate (GST TCS plus section 194-O TDS) applied to the menu price.
//
// The quantity on the item is not used: this is a per-unit figure, so that two
// dishes can be compared. Use WorstDishes to roll it up over a period.
func DishMargin(it Item, foodCost int64, commissionPct BasisPoints, packaging int64, taxRate BasisPoints) DishEconomics {
	d := DishEconomics{
		Name:        strings.TrimSpace(it.Name),
		MenuPrice:   it.UnitPrice,
		FoodCost:    foodCost,
		Commission:  ApplyRate(it.UnitPrice, commissionPct),
		Packaging:   packaging,
		TaxWithheld: ApplyRate(it.UnitPrice, taxRate),
	}
	d.NetRealisation = d.MenuPrice - d.Commission - d.Packaging - d.TaxWithheld
	d.GrossMargin = d.MenuPrice - d.FoodCost
	d.NetMargin = d.NetRealisation - d.FoodCost
	if pct, ok := EffectiveRate(d.GrossMargin, d.MenuPrice); ok {
		d.GrossMarginPct = pct
	}
	if pct, ok := EffectiveRate(d.NetMargin, d.MenuPrice); ok {
		d.NetMarginPct = pct
	}
	d.LossMaking = d.NetMargin < 0
	d.FlattersToDeceive = d.GrossMargin > 0 && d.NetMargin < 0
	return d
}

// DishRoll is one dish's economics over a whole period.
type DishRoll struct {
	DishEconomics
	// Units is how many were sold across the imported orders.
	Units int `json:"units"`
	// Revenue is Units x MenuPrice.
	Revenue int64 `json:"revenue"`
	// TotalNetMargin is Units x NetMargin: the money this dish made, or lost,
	// over the period. Sorting on this is what identifies the dish that is
	// quietly costing the most.
	TotalNetMargin int64 `json:"totalNetMargin"`
	// TotalGrossMargin is Units x GrossMargin, for the contrast.
	TotalGrossMargin int64 `json:"totalGrossMargin"`
	// CostKnown is false when no food cost was entered for this dish, in
	// which case the margins are measured against a zero cost and are
	// therefore an upper bound, not an answer.
	CostKnown bool `json:"costKnown"`
}

// DishKey is the lookup key for a dish's food cost: trimmed and upper-cased,
// so that "Paneer Butter Masala" and "paneer butter masala " are one dish.
func DishKey(name string) string { return strings.ToUpper(strings.Join(strings.Fields(name), " ")) }

// WorstDishes rolls every item line in the imported orders into one row per
// dish and sorts them worst-first by the money that dish lost over the period.
//
// costs maps DishKey(name) to the food cost of one unit, in paise. A dish with
// no entry is reported with CostKnown false rather than left out, because a
// missing cost is itself something the owner should fix.
//
// Packaging is attributed to a dish pro rata by menu value: an order's
// packaging charge is split across its item lines in proportion to their
// value, which is the only split that does not require the owner to tell the
// tool which dishes needed a container.
func WorstDishes(orders []Order, costs map[string]int64, opts Options) []DishRoll {
	type acc struct {
		name      string
		units     int
		value     int64 // total menu value sold
		packaging int64 // total packaging attributed
	}
	byDish := map[string]*acc{}
	keys := []string{}

	for _, o := range orders {
		// Total menu value of the lines, for the pro-rata packaging split.
		var lineTotal int64
		for _, it := range o.Items {
			lineTotal += it.LineValue()
		}
		var packagingLeft int64
		if opts.Rates.PackagingBorneByRestaurant {
			packagingLeft = o.PackagingCharge
		}
		for i, it := range o.Items {
			if strings.TrimSpace(it.Name) == "" || it.Qty <= 0 {
				continue
			}
			k := DishKey(it.Name)
			a, ok := byDish[k]
			if !ok {
				a = &acc{name: strings.Join(strings.Fields(it.Name), " ")}
				byDish[k] = a
				keys = append(keys, k)
			}
			a.units += it.Qty
			a.value += it.LineValue()
			// Give the last line the remainder so the split is exact.
			var share int64
			if i == len(o.Items)-1 || lineTotal <= 0 {
				share = packagingLeft
			} else {
				share = ApplyRateOf(packagingLeft, it.LineValue(), lineTotal)
			}
			packagingLeft -= share
			a.packaging += share
		}
	}

	rate := opts.Rates.GSTTCS + opts.Rates.TDS194O
	out := make([]DishRoll, 0, len(keys))
	for _, k := range keys {
		a := byDish[k]
		if a.units == 0 {
			continue
		}
		unitPrice := a.value / int64(a.units)
		unitPack := a.packaging / int64(a.units)
		cost, known := costs[k]
		d := DishMargin(Item{Name: a.name, Qty: 1, UnitPrice: unitPrice}, cost, opts.AgreedCommissionPct, unitPack, rate)
		out = append(out, DishRoll{
			DishEconomics:    d,
			Units:            a.units,
			Revenue:          a.value,
			TotalNetMargin:   d.NetMargin * int64(a.units),
			TotalGrossMargin: d.GrossMargin * int64(a.units),
			CostKnown:        known,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalNetMargin != out[j].TotalNetMargin {
			return out[i].TotalNetMargin < out[j].TotalNetMargin
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ApplyRateOf apportions amount in the ratio part:whole, rounded half away
// from zero. It is used for the pro-rata packaging split.
func ApplyRateOf(amount, part, whole int64) int64 {
	if whole == 0 || amount == 0 || part == 0 {
		return 0
	}
	n := amount * part
	if (n < 0) != (whole < 0) {
		return (n - whole/2) / whole
	}
	return (n + whole/2) / whole
}
