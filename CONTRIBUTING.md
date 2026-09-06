# Contributing to Gauge

Gauge measures economic risk to Stellar liquidity positions. It is a measurement
engine, which means its credibility rests entirely on whether its numbers are
right — not on how many features it has. That shapes what contributions are
wanted and, more unusually, what contributions are not.

## Getting set up

You need Go 1.22 or newer. Nothing else yet; the Python metrics layer does not
exist at the time of writing, and this file will grow a section when it does.

```sh
make build   # binaries into bin/
make test    # the suite
make race    # the suite under the race detector, as CI runs it
make vet     # go vet
make fmt     # gofmt
```

## What is maintainer-owned

Most of this repository is ordinary open source and contributions are welcome on
the usual terms. Four things are not, and this is stated up front so that nobody
spends a weekend on a pull request that was never going to be merged:

1. **The metric formulas.** Impermanent loss, realised volatility, concentration,
   drawdown, fee yield, net P&L. Each one is written out longhand in source with
   its derivation in a comment, verified by hand against known cases, and
   recorded in `docs/metrics-verification.md`. A pull request that replaces one
   with a library call, however equivalent, will be declined — the visibility of
   the arithmetic *is* the feature.
2. **The label methodology.** How a realised position outcome is defined, which
   effects establish entry basis, and what happens to positions whose history
   predates the ingested window. This determines what every downstream number
   means.
3. **The risk score composition.** How individual metrics combine into anything
   presented as a single score. A composition is a set of value judgements about
   what matters, and those are owned rather than delegated.
4. **The data survey's findings.** `docs/data-survey.md` reports what the
   population actually looks like. Corrections backed by a reproducible run are
   very welcome. Rewrites that soften a finding are not.

None of these exist yet as code. The list is written before them deliberately,
so the boundary is a stated policy rather than a reaction to a specific PR.

## What is wanted

- **Ingestion.** Endpoint coverage, pagination edge cases, retry behaviour,
  anything that makes fetching real data more honest or more reliable.
- **Test fixtures.** Recorded Horizon responses, captured from mainnet, in
  `testdata/`.
- **Bug reports with a reproduction.** Especially arithmetic that disagrees with
  a hand calculation. That is the most valuable issue this project can receive.
- **Documentation** that makes a number easier to check.

## Two hard rules

**Exact arithmetic in the accounting path.** Every monetary value, reserve,
share balance and position figure is `decimal.Decimal` (Go: `shopspring/decimal`)
from the first line of ingestion. Floats are permitted only inside the eventual
statistical layer, and never where a number is presented as money. A PR
introducing `float64` into `pkg/horizon`, `cmd/ingest`, or any accounting path
will be declined on sight.

**No fabricated data, ever.** No synthetic positions, no illustrative figures, no
plausible-looking example numbers in documentation. Every figure in this
repository traces to real ingested mainnet data, and any figure that cannot be
computed is reported as missing rather than filled in. Test fixtures are
recorded real responses, not hand-written ones.

## Dependencies

Each third-party dependency must be justified — in the commit message that adds
it, or in a document — with a reason a reader can disagree with. "It is popular"
is not a reason. At the time of writing the entire dependency set is
`github.com/shopspring/decimal`, because the float ban makes an exact decimal
type non-optional and this is the one proven elsewhere in this portfolio.

Horizon is JSON over HTTP; `net/http` and `encoding/json` cover it. The Stellar
Go SDK was considered and declined: it is a large dependency tree for two
endpoints.

## Commit messages

Describe what happened and what was learned, not what was added. "discovered
Horizon returns 503 intermittently on the accounts-by-pool filter" is a good
commit message. "add ingestion layer" is not.
