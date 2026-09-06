# Metrics verification

Every formula in [`pkg/metrics/`](../pkg/metrics/) checked against a case whose
answer exists independently of this code. Where the check is against a published
figure, the source is named. Where it is against an exact rational, the
arithmetic is shown so a reader can do it on paper.

Reproduce with:

```sh
make py-test          # 55 tests
```

**Nothing here is verified against Gauge's own output.** A formula checked only
against itself is checked against nothing.

---

## 1. Impermanent loss

    IL(r) = 2*sqrt(r)/(1+r) - 1

Derived in full in the module docstring of
[`impermanent_loss.py`](../pkg/metrics/gauge_metrics/impermanent_loss.py) from
the constant-product invariant, not quoted.

### Against the published table

The canonical values quoted throughout the AMM literature and in Uniswap's own
documentation on divergence loss:

| Price ratio `r` | Published IL | Gauge | Agrees |
|---|---:|---:|:--:|
| 1.00 | 0% | `0.0000%` | ✓ |
| 1.25 | 0.6% | `-0.6192%` | ✓ |
| 1.50 | 2.0% | `-2.0204%` | ✓ |
| 2.00 | 5.7% | `-5.7191%` | ✓ |
| 3.00 | 13.4% | `-13.3975%` | ✓ |
| 4.00 | 20.0% | `-20.0000%` | ✓ |
| 5.00 | 25.5% | `-25.4644%` | ✓ |

### Against exact rationals

Two points on the curve have closed-form answers, so an implementation error
cannot hide behind rounding:

- **r = 1.** `2*sqrt(1)/(1+1) - 1 = 2/2 - 1 = 0`.
  Gauge returns `0` — the Decimal zero, not `1e-30`.
- **r = 4.** `2*sqrt(4)/(1+4) - 1 = 4/5 - 1 = -1/5`.
  Gauge returns exactly `-0.2`. Verified as an equality, not a tolerance.

### Against its own symmetry

`IL(r) = IL(1/r)` — a doubling and a halving cost the same. This is a property
of the formula that a wrong implementation is unlikely to reproduce by accident.

| | IL |
|---|---:|
| `IL(2)` | `-0.0571909584179366341322075171935346142868854164154` |
| `IL(0.5)` | `-0.0571909584179366341322075171935346142868854164154` |
| `IL(4)` | `-0.2` |
| `IL(0.25)` | `-0.2` |

Identical to all 49 digits carried. Also checked at r = 1.5, 10, 100.

### Sign and monotonicity

IL is never positive at any ratio — asserted in the function itself, so a
positive result raises rather than being reported as a windfall — and loss grows
monotonically as price moves away from entry in either direction. Both are
tested.

---

## 2. Net position P&L, cross-checked against impermanent loss

The strongest check in this document, because it compares two formulas that were
derived and implemented separately.

Net P&L is computed as value-now versus value-if-held, from pool composition.
Impermanent loss is computed from the price ratio. **With no fees earned, they
must be the same number**, because that is the definition of IL. They are not
implemented in terms of each other.

A position holding 10% of a pool that starts at 1000/1000 (`k` = 1e6) after the
price of A in B doubles:

| | |
|---|---:|
| Value now (in B) | `282.84271247461900976033774484194` |
| Value if simply held | `300.00000000000000000000000000001` |
| Net P&L fraction | `-0.05719095841793663413220751719357` |
| Impermanent loss | `-0.05719095841793663413220751719354` |
| **Difference** | **`3.2e-32`** |

Agreement to 31 decimal places. The residual is the working precision of the
irrational `sqrt` in the hand-specified reserves, roughly 25 orders of magnitude
below a stroop.

Both also land on the published `-5.72%` for a 2× move, which ties this check
back to section 1.

---

## 3. Fee yield

    fee_yield = (sqrt(k)/shares)_now / (sqrt(k)/shares)_entry - 1

Fees in a constant-product AMM are retained in the reserves rather than paid
out, so they raise `k` for a fixed share supply. `sqrt(k)/shares` is the
price-independent per-share size of the pool.

Two checks:

- **A pure price move earns nothing.** Taking the same pool from 1000/1000 to
  the price-2 composition leaves `k` unchanged, so `sqrt(k)/shares` is unchanged.
  Gauge returns a value below `1e-25` — zero to working precision.
  This matters: an implementation that confused price movement with fee income
  would report a large fictitious yield here.
- **A 1% rise in `k` yields 0.5%.** Reserves both up 0.5% at unchanged price
  gives `sqrt(k)` up 0.5%. Gauge returns exactly `0.005`.

---

## 4. Concentration (HHI)

    HHI = sum of s_i^2

| Holders | Hand calculation | Gauge |
|---|---|---:|
| two equal | `0.5² + 0.5² = 0.5` | `0.50` |
| four equal | `4 × 0.25² = 0.25` | `0.2500` |
| five equal | `5 × 0.2² = 0.2` | `0.2` |
| 90 / 10 | `0.9² + 0.1² = 0.81 + 0.01 = 0.82` | `0.82` |

Scale invariance is checked (`1:3` equals `1000000:3000000`), as is exclusion of
zero-balance trustlines.

### What the data does not support, stated explicitly

The brief asked for this to be recorded, and the survey settled it by
measurement rather than assumption:

- **Account-level attribution is genuine here, not a proxy.** Holder balances
  come from `GET /accounts?liquidity_pool=<id>`, which returns real account IDs
  and exact share balances. This is not a coarser level of detail.
- **But it is unavailable for most of the network.** 34,402 of 39,833 pools
  (86.37%) have a single trustline, where HHI is 1.0 by construction.
  `holder_hhi` returns `None` for those rather than a misleading 1.0.
- **And it requires a complete holder set.** Positions arrive both from pools
  deliberately attributed and from pools discovered incidentally in another
  account's balance list; the second kind is an arbitrary subset. The caller
  must pass `complete=False` for those and gets `None`. A concentration
  computed over an unknown fraction of a pool is not a weaker measurement, it
  is a wrong one.
- **A trustline is not a holder.** 6.58% of positions carry a zero balance.
  They are excluded from both numerator and holder count.

Where it *is* available it is informative rather than constant: across the 208
in-scope pools the largest holder's share runs 0.0516 to 1.0000, median 0.7997.

`top_holder_share` is reported alongside HHI because a long tail of small
holders lowers HHI without lowering single-point-of-failure risk — tested with a
95% holder plus forty 0.125% holders, where HHI reads below 0.91.

---

## 5. Drawdown

    drawdown_t = value_t / running_peak_t - 1

| Series | Expected | Gauge |
|---|---|---:|
| `100, 120, 60, 80` | peak 120 → trough 60 = `-0.5` | `-0.5` |
| `100, 50, 200` | `-0.5`, **not** `-0.75` | `-0.5` |
| `1, 2, 3` | `0` (a real zero) | `0` |
| `3, 1` | `1/3 - 1` exactly | `-0.666…` |

The second row is the case that catches a naive implementation. Taking
`min/max - 1` over the whole series reports `-75%` — a decline that never
happened, because the trough precedes the peak. The running peak must be tracked
forward in time.

Drawdown stays in exact decimal arithmetic: it is a ratio of two observed values
and needs no transcendental function, so it does not get a float.

---

## 6. Realised volatility

Standard deviation of log returns, Bessel-corrected, optionally annualised by
`sqrt(periods per year)`.

**This is the one metric that returns a `float`, deliberately.** A logarithm has
no exact decimal representation, and the input is already a sample — a
volatility computed from whatever moments a census happened to observe is a
statistic about that sample, and float64's sixteen digits are orders of
magnitude finer than the sampling error. The return type is the documentation:
it is never formatted as money.

Checks:

- A flat series returns exactly `0.0`.
- Annualising by daily spacing (17,280 ledgers) scales the result by
  `sqrt(365)` to within `1e-3`.
- Fewer than three observations returns `None`, not zero. Two points give one
  return and a sample standard deviation of zero, which would report a volatile
  pool as perfectly calm.

---

## 7. Trade price normalisation

Not a metric, but the input to two of them, and the place a plausible-looking
series goes wrong silently.

Horizon reports each trade from the taker's side: `price` is counter/base, and
which asset is the base flips between trades in the same pool. A series built
without normalising alternates between `p` and `1/p`.

The check is against an independent measurement of the same quantity — the
pool's own reserve ratio:

- Pool: `a468d41d…` (native/USDC), the largest on the network.
- Reserves at the time of recording: `12,286,463.1416566` XLM against
  `2,295,481.1143391` USDC.
- Reserve ratio, price of A in B: **0.1868300981229003**.
- All 20 real trades in the fixture normalise to within 20% of that figure.

The inverted form would be `5.4`, off by a factor of about 29 — so this test
distinguishes the two decisively rather than marginally. A pool whose price is
genuinely flat would otherwise report several hundred percent volatility.

Prices are taken from Horizon's exact `n`/`d` rational rather than by dividing
the two amount strings, so the value stays exactly as the ledger expressed it and
no float appears in the path.

---

## 8. Dust trades, and why drawdown needed protecting from them

Section 7 verified the series points the right way. It says nothing about
whether every observation in it is a price, and one class of them is not.

Stellar stores amounts as integers of 1e-7. A swap of **one stroop for one
stroop** reports `price` as the rational `1/1`, so the observation comes back as
exactly 1 no matter what the pool is actually worth. The rational cannot express
the price at that resolution.

This was found by disbelieving a number rather than by testing for it. The worst
drawdown across the in-scope population was **-99.98%**, on the native/LUSD pool:

| | |
|---|---|
| Pool trades near | `5958` (its reserve ratio) |
| Series | `5986.7` → peak `6019.7` → **trough `1`** → `5938.3` |
| Offending trade | `275999958560165889-0`, 2026-09-03T23:17:47Z |
| Base amount | `0.0000001` XLM |
| Counter amount | `0.0000001` LUSD |
| Reported price | `n/d` = `1/1` = **1** |

Drawdown is maximally sensitive to a single outlier — it is a max over the
series — so one such trade is enough to destroy the figure, and the result was
plausible-looking enough to publish.

**The fix, and where it lives.** Trades now carry their base and counter amounts
through ingestion, and the metrics layer excludes observations where either side
is at the resolution floor. Ingestion records what the ledger said and filters
nothing; the judgement about which observations are informative belongs in the
metrics layer, where the threshold is visible and testable rather than buried in
a fetch. `price_series_for(drop_dust=False)` still returns the raw series and
`dust_count` reports what was removed, so the filter can always be audited.

**The threshold is set by precision, not by size**, and the first attempt got it
wrong. Excluding trades at or below ten stroops caught the degenerate `1/1` and
`2/1` cases and let a whole family through: twelve stroops against thirteen
reports `13/12` = 1.0833…, which looks like a plausible price and is only the
finest thing a twelve-stroop trade can say.

The rule now follows from the quantisation directly: a swap of N stroops reports
a rational granular at 1/N, so 100 stroops bounds the relative error at 1% —
finer than any move these metrics are meant to detect. That is 1e-5 XLM, a few
millionths of a cent, so nothing with economic content is excluded; what is
excluded is only the range where the ledger's resolution prevents a price from
being stated. A trade with no recorded amounts — from runs made before amounts
were carried — is not treated as dust, because absent information must not
masquerade as a judgement. All of these cases are tested.

### How much it mattered

Over 113,893 trades from 205 in-scope pools:

| | |
|---|---:|
| Excluded as unpriceable | 4,699 (4.13%) |
| Pools containing at least one | 51 of 205 (24.9%) |
| Pools whose drawdown moved by over a point | 19 |
| Worst drawdown, raw → filtered | −99.98% → **−74.76%** |
| Highest volatility, raw → filtered | 0.4655 → **0.0959** |
| Median drawdown, raw → filtered | −3.12% → −2.65% |
| Median volatility, raw → filtered | 0.00238 → 0.00231 |

The worst drawdown was overstated by a factor of 15 and the highest volatility
by 5, while the medians barely moved. That is the shape of the hazard: the
aggregate looked reasonable throughout, and only individual pools were wrong.

---

## What is *not* verified here

- **No metric is checked against a live Horizon computation**, because Horizon
  computes none of these. There is no upstream figure to agree with.
- **Nothing is checked against a real position's realised outcome**, and the
  survey now quantifies why. Horizon retains 365 days of history, 81.91% of
  in-scope positions last moved before that boundary, and a sampled effects walk
  over the remainder found that fewer than half of *those* were actually opened
  inside the window. The estimate is that about **8.68% of in-scope positions
  (95% interval 5.14%–12.22%) have a complete, recoverable entry basis** — of
  order a thousand positions.

  The formulas are verified against known cases. Computing them over those
  thousand positions is real work that this repository can now do, and it is the
  obvious next step; until it is done, the CLI reports P&L as unavailable rather
  than computing it from an assumed basis.
- **Realised volatility and drawdown were, until trade ingestion existed, only
  checked against constructed series**, because a census gives one price per
  pool and one point has no variance. `cmd/ingest -trades N` now builds a real
  price series per pool, the normalisation that makes it meaningful is itself
  verified against an independent measurement (section 7), and the observations
  are filtered for a quantisation artefact that would otherwise ruin drawdown
  (section 8).
