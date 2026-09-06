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

Early. This repository is at the ingest-and-survey stage: it fetches real pool
and position data and characterises the population. No metrics are implemented
yet, deliberately — a metric computed over a population nobody has described is
decorative. `docs/data-survey.md` comes first.

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

## Documentation

| Document | What it is |
|---|---|
| [docs/positioning.md](docs/positioning.md) | What Gauge measures that adjacent projects do not, with the evidence |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to contribute, and what is maintainer-owned |

## Licence

Apache-2.0. See [LICENSE](LICENSE).
