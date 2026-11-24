# Aggregator payout reconciler

**Food-aggregator payout reconciler for restaurants and cloud kitchens.** A single Go binary, with no dependencies, that reconciles a delivery platform's settlement statement against your own order book — and then shows you what each dish really earns once commission comes out.

Part of **Open MSME**: free tools for micro and small businesses in India and the Philippines. [All tools](https://sreenivas-sadhu-prabhakara.github.io/openmsme/)

## What it does

A delivery aggregator settles one lump sum after deducting commission, a payment-gateway fee, packaging and tax across hundreds of orders. You know the total that reached your bank. You cannot tell whether it is right.

Give this tool two CSV files — your order export and the platform's settlement statement — and it:

- matches them order by order and computes what each order **should** have paid from the terms in your agreement;
- sorts every order into **settled correctly** (within a tolerance you set), **short-paid**, **over-paid**, or **in your books but never settled**;
- flags **commission overcharges**, where the commission charged implies a higher rate than your contract allows, and reports the rupee impact on each order;
- lists **settlement lines with no matching order** in your books, which usually means your own records are incomplete;
- computes the **true margin on every dish** after commission, packaging and withholding, sorted worst-first, flagging dishes that look profitable on the menu card and lose money on every order;
- shows one headline figure, **total money owed back**;
- exports each category as a CSV, plus a **dispute list** with the arithmetic shown line by line.

It also reports every row it could not read, with the line number and the offending cell. Nothing is dropped quietly.

**It does not send anything to anyone.** The dispute list is a file for you to check and send yourself.

## Who it's for

Restaurants and cloud kitchens selling through delivery aggregators, in India and the Philippines. A single outlet checking one month, or a small group reconciling several outlets one statement at a time. No accounting software needed: two CSV exports and the commission rate from your contract are enough.

## The regulation it implements

Two Indian withholding provisions apply to money a delivery platform collects on your behalf. Both are **named constants in one file** (`internal/payout/rates.go`) and both are **editable in the tool**.

| Provision | Rate used | Source and date |
|---|---|---|
| **GST TCS, section 52, CGST Act, 2017** | `GSTTCSSection52Ceiling` = **1.00%** | The statutory ceiling ("not exceeding one per cent" of the net value of taxable supplies); also the notified combined rate from 1 October 2018 to 9 July 2024 |
| | `GSTTCSSection52Notified` = **0.50%** | Combined rate notified with effect from 10 July 2024 (Notification 15/2024-Central Tax and the matching integrated-tax notification): 0.25% CGST + 0.25% SGST/UTGST, or 0.5% IGST |
| **TDS, section 194-O, Income-tax Act, 1961** | `TDSSection194O` = **0.10%** | Of the gross amount of sales, with effect from 1 October 2024 (reduced from 1%). Assumes the platform holds your PAN; without one, section 206AA imposes a higher rate |
| | `TDSSection194OUntilSep2024` = **1.00%** | The rate up to 30 September 2024, kept so an older period still reconciles |

**The carve-out that matters most to a restaurant.** Restaurant service supplied through an e-commerce operator was notified under **section 9(5)** of the CGST Act by Notification 17/2021-Central Tax (Rate), with effect from **1 January 2022**. The operator pays that GST itself, and **CBIC Circular 167/23/2021-GST** states that the operator is therefore not required to collect TCS under section 52 on it. So for cooked-food orders the expected GST TCS is **nil** — which is what the tool's default rate set (`RatesIndiaRestaurant`) assumes. TCS does still apply to anything you sell through the platform that section 9(5) does not cover. TDS under section 194-O applies either way.

**For the Philippines the tool asserts no withholding rate.** The regime for online platform operators differs from the Indian one and is not a figure worth guessing, so `RatesPhilippines` withholds nothing and says so. Everything else still reconciles in full; if a withholding applies to you under a BIR issuance, enter that rate and the calculation follows.

Rates change. Confirm the figures in force for the period you are reconciling with the authority or your accountant — the tool lets you override every one.

## How the expected payout is defined

```
expected payout = gross value
                - restaurant-funded discount
                - commission  (agreed % x commissionable base)
                - payment-gateway fee
                - packaging   (only if the restaurant bears it)
                - tax withheld (GST TCS + s.194-O TDS)

commissionable base = gross value - restaurant-funded discount
```

The commissionable base is defined in exactly one place (`CommissionableBase` in `internal/payout/order.go`) and used by every rule. Three decisions are built into it:

- The **platform-funded discount is not deducted.** When the platform buys a promotion for the customer, you sold at full price and commission belongs on full price. A statement that charges commission on the pre-discount value while paying out on the post-discount value is the commonest way a payout goes wrong.
- The **restaurant-funded discount is deducted.** That money was never earned.
- **Packaging, taxes and the delivery fee are outside the base.** They are not your sale.

Both withholding rates are applied to that same base. That is a documented simplification: section 52 speaks of the net value of taxable supplies and section 194-O of the gross amount of sales.

A commission overcharge is reported separately but is **not added** to the money owed back. Because the expected payout already uses your agreed rate, every rupee of excess commission is already inside the short payment; adding it again would overstate the claim.

## Install and run

```sh
git clone https://github.com/sreenivas-sadhu-prabhakara/aggregator-payout-reconciler.git
cd aggregator-payout-reconciler

go run .                                      # run it straight away
go build -o aggregator-payout-reconciler .    # or keep the single binary
./aggregator-payout-reconciler
```

Then open **http://127.0.0.1:8080/**. Press `Ctrl-C` to stop.

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | Address to listen on. Keep it on the loopback interface. |
| `-data` | `./aggregator-payout-reconciler-data.json` | Where to keep imported statements and settings. |

There is a worked example built in — click **Load a worked example** to see the whole thing work before importing your own files. Its figures are illustrative, not real trading data.

### The files it reads

**Order export.** Needs an order reference, and either a gross value column or item columns. One row per order, or one row per dish line with the reference repeated — the shape is detected from the header. Optional: date, restaurant-funded discount, platform-funded discount, packaging charge, item name, quantity, unit price.

**Settlement statement.** Needs an order reference and a net settled (or payout) column. Optional: commission, payment-gateway fee, and withholding as either one combined column or separate TCS and TDS columns, which are summed.

**Food costs (optional).** Two columns: dish name, and the food cost of one unit. This is what turns on the per-dish margin table.

Column names are matched however the export spelled them — case, spaces, underscores, hyphens, hash markers, currency signs and suffixes, a byte-order mark. Deductions are read as deductions whether the statement signs them positive, negative or in accounting brackets. Ragged rows, stray quotes and Windows line endings are tolerated. Anything genuinely unreadable is reported with its line number rather than skipped in silence.

## Where your data is stored

One JSON file on your own machine: `./aggregator-payout-reconciler-data.json`, or wherever `-data` points. It holds the orders and settlement lines you imported, the food costs you entered and your contract terms. It is written with owner-only permissions, and written atomically (temporary file plus rename) so an interrupted save never truncates good data. Read it, edit it, back it up or delete it as you like.

**Nothing leaves the machine.** No outbound network calls of any kind: no telemetry, no analytics, no external fonts or scripts, no update check. It binds to `127.0.0.1` only, so it is not reachable from your network, and it refuses any request that did not come from its own page. It cannot contact the delivery platform and does not try.

## Scope and accuracy

This tool does arithmetic on two files you supply, against contract terms you enter. Read that sentence literally, because it bounds everything:

- It is **not tax advice**. It does not read your agreement and cannot know a term you have not told it. If your contract defines the commissionable base differently from the definition above, the effective-commission figures will differ from your contract by exactly that difference.
- It cannot tell a **genuine deduction from a wrong one**. A variance is a question to ask the platform, not proof of anything. Check a claim yourself before sending it.
- **Statutory rates change.** Every rate is dated and sourced above and overridable in the tool, but confirming the rate in force for your period is your call, not the tool's.
- Per-dish margins are only as good as the **food cost you enter**. A dish with no cost entered is shown with its margins measured against zero, and labelled as an upper bound rather than an answer.
- Packaging is attributed to a dish **pro rata by menu value**, because that is the only split that does not require you to tell the tool which dishes needed a container.
- Where one order was settled over **several statement rows**, those rows are summed, because that is what the bank received. Where your books record the same reference **twice**, the first is reconciled and the duplicate is reported rather than merged.

## Development

```sh
gofmt -l .              # must print nothing
go vet ./...
go test ./... -count=1
go build -o /tmp/aggregator-payout-reconciler-bin .
```

Every rule lives in `internal/payout` as a pure function with table-driven tests, so the arithmetic is testable without HTTP. The standard library is the only dependency; `go.mod` has no `require` block.

MIT licensed.
