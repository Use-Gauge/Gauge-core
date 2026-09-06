# Gauge

A quantitative risk-measurement engine for Stellar liquidity positions.

Gauge answers one question about a liquidity position: **did this capital lose
money, and how badly.** It reads real mainnet AMM pool and liquidity-position
data from Horizon and computes deterministic metrics over it — impermanent loss,
realised volatility, concentration, drawdown, fee yield, net P&L.

It does not classify behaviour, and it does not predict. Where a number cannot
be computed from the ledger, Gauge reports it as missing rather than estimating
it. See [docs/positioning.md](docs/positioning.md) for how this differs from
fraud detection on the same pools, and why the difference matters.

## Status

Early, and deliberately so. This repository is at the ingest-and-survey stage:
it fetches real pool and position data and characterises the population. No
metrics are implemented yet — a metric computed over a population nobody has
described is decorative, so [the survey](docs/data-survey.md) came first.

What the survey found, in three lines:

- There are **39,833** AMM pools on Stellar mainnet. One of them holds **54.37%**
  of all pooled XLM.
- Filtering to pools with enough value to matter *and* more than one holder
  leaves **208 pools** — 0.52% of the population.
- **3.86%** of the monetary values in the census do not survive a `float64`
  round trip, which is why the decimal rule below is enforced by CI rather than
  by good intentions.

## Invariants

- **`decimal.Decimal` for every monetary value**, from the first line of
  ingestion. Floats are confined to the eventual statistical layer and never
  appear in an accounting path. CI enforces this.
- **No fabricated data.** Every figure in this repository traces to a real
  ingested mainnet run, including the ones in the documentation.

## Building

```sh
make build   # binaries into bin/
make race    # the test suite as CI runs it
make help    # all targets
```

## Reproducing the survey

```sh
./bin/ingest -out data/runs                          # pool census: ~200 requests, ~60s
./bin/ingest -out data/runs -holders                 # full attribution: ~7 hours
./bin/ingest -out data/runs -holders -min-native 1000  # in-scope only: minutes
./bin/survey data/runs/<run>                         # the figures in the survey
```

Ingested runs are not committed by default — they are large and reproducible
from the commands above. The manifests are, under [docs/runs/](docs/runs/).

## Metrics

```sh
make metrics RUN=data/runs/<run>
```

Impermanent loss, concentration, fee yield, drawdown, realised volatility and
net P&L — each written out longhand in [pkg/metrics/](pkg/metrics/) with its
derivation, and verified against independently-known cases in
[docs/metrics-verification.md](docs/metrics-verification.md).

Quantities the ledger cannot support are printed as `-`, never estimated. That
is most of them: Horizon retains 365 days of history and **81.91% of in-scope
positions last moved before that boundary**, so their entry basis is
unrecoverable and their P&L is genuinely unknowable rather than zero.

## Documentation

| Document | What it is |
|---|---|
| [docs/data-survey.md](docs/data-survey.md) | What is actually in the data. The document everything else rests on |
| [docs/metrics-verification.md](docs/metrics-verification.md) | Every formula, checked against a case whose answer exists independently |
| [docs/positioning.md](docs/positioning.md) | What Gauge measures that adjacent projects do not, with the evidence |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to contribute, and what is maintainer-owned |

## Licence

Apache-2.0. See [LICENSE](LICENSE).
