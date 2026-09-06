# The data survey

What is actually in Stellar's AMM pool and liquidity-position data, measured
rather than assumed, before Gauge computes a single metric over it.

This document exists because a metric computed over a population nobody has
described is decorative. Every number below comes from a committed run and can
be re-derived; where a number could not be obtained, it says so instead of
being filled in.

```sh
make build
./bin/ingest -out data/runs            # the census
./bin/ingest -out data/runs -holders   # the census plus position attribution
./bin/survey data/runs/<run>           # the figures below
```

**Runs cited:** `20260906T063358Z` (census) and `20260906T063725Z` (census plus
attribution), both against `https://horizon.stellar.org`, 2026-09-06.

---

## Population

| | |
|---|---:|
| Pools | **39,833** |
| Pages | 200 |
| HTTP requests | 201 |
| Wall clock | 63 seconds |
| Ledger range touched | 38,115,941 – 64,296,875 |

The first thing the census established is that the sampling probe that preceded
it was wrong about the size of the population. That probe walked 40 pages and
reported 8,000 pools; it had not reached the end of the listing and had no way
to know it. The real figure is **five times larger**. A sample terminated by its
own page cap is a sample of nothing in particular, and it is recorded here
because the planning for this session was done on that number.

A census is not a snapshot. It took 63 seconds and 201 requests, during which the
chain kept moving, so the manifest records `ledger_low` and `ledger_high` rather
than stamping one timestamp on the result. The 26.2 million ledgers between the
least and most recently modified pool are not a measurement artefact — they are
the actual spread of when these pools were last touched.

### Uniformity

Every pool in the population is `constant_product` with `fee_bp` 30. There is no
variation to model, no fee tier to condition on, and no pool type to branch on.
Both fields are carried through ingestion anyway, so that a future protocol
change shows up as data rather than as a violated assumption buried in code.

---

## Distribution

### Holders: the population is overwhelmingly single-holder

`total_trustlines` is Horizon's holder count and the only one available without
a request per pool.

| Trustlines | Pools | Share |
|---|---:|---:|
| exactly 1 | 34,402 | **86.37%** |
| exactly 2 | 2,999 | 7.53% |
| exactly 3 | 978 | 2.46% |
| ≥ 2 | 5,431 | 13.63% |
| ≥ 5 | 1,077 | 2.70% |
| ≥ 10 | 430 | 1.08% |
| ≥ 100 | 39 | 0.10% |

Median 1, p90 2, p99 10, max 2,418. Across all 39,833 pools there are 76,979
trustlines in total — fewer than two per pool.

This has a direct consequence for one of the metrics this project intends to
build. **Cross-holder concentration is undefined or trivial for 86.37% of the
population**: an HHI over holders in a single-holder pool is 1.0 by
construction, and reporting that as a risk measurement would be measuring the
arithmetic rather than the market.

### Value: concentrated past the point of caricature

There is no USD denominator on-chain (see [what is missing](#what-is-missing-or-unusable)),
so value is measured on the two legs where a common unit exists.

**By XLM leg** — 10,100 pools, 22,596,672.21 XLM pooled in total:

| | Share of all pooled XLM |
|---|---:|
| Top 1 pool (`native`/`USDC`) | **54.37%** |
| Top 5 | 77.82% |
| Top 10 | 86.91% |
| Top 25 | 92.61% |
| Top 100 | 96.98% |
| Top 1,000 | 99.86% |

**By USDC leg** — 885 pools, 2,572,813.68 USDC pooled:

| | Share of all pooled USDC |
|---|---:|
| Top 1 pool | **89.22%** |
| Top 5 | 95.32% |
| Top 10 | 97.66% |
| Top 50 | 99.60% |

The same pool sits at the top of both. It holds 12,286,463.14 XLM against
2,295,481.11 USDC, has 838 trustlines, and by itself is more than half of every
XLM in every AMM pool on the network.

The other end of the distribution:

| XLM-legged pools holding | Count | Share |
|---|---:|---:|
| < 1,000 XLM | 9,784 | 96.87% |
| < 100 XLM | 9,311 | 92.19% |
| < 10 XLM | 8,285 | 82.03% |
| < 1 XLM | 5,785 | **57.28%** |
| < 0.1 XLM | 3,665 | 36.29% |

52.1% of USDC-legged pools hold less than one USDC.

### The ten largest pools by XLM leg

| XLM | Pair | Trustlines |
|---:|---|---:|
| 12,286,463.14 | native/USDC | 838 |
| 2,159,793.87 | native/yXLM | 662 |
| 1,910,138.17 | native/SHX | 717 |
| 626,160.38 | native/BTCLN | 45 |
| 601,840.43 | native/EURC | 41 |
| 534,404.52 | native/VELO | 242 |
| 521,509.90 | native/AQUA | 2,418 |
| 397,304.16 | native/BTC | 69 |
| 345,165.15 | native/XRP | 232 |
| 256,792.80 | native/MTL | 7 |

Note the seventh row. `native`/`AQUA` has by far the most holders of any pool on
the network — 2,418, nearly three times the largest pool's 838 — while holding a
twenty-fourth as much XLM. Holder count and size are not the same axis, and a
survey that reported only one of them would have missed that.

### Assets: a very long tail

- 19,861 distinct assets across 39,833 pools.
- **11,356 assets (57.2%) appear in exactly one pool.**
- 39,833 pools, 39,833 distinct pairs — **no pair is duplicated**. A
  constant-product pool's identity is derived from its asset pair and fee, so
  the population is exactly the set of pairs anyone has ever created.
- 10,100 pools (25.36%) have an XLM leg.
- 5,122 pools (12.9%) are pairs where *both* assets appear in three pools or
  fewer.

The most-paired assets are not the ones a reader would guess:

| Asset | Pools |
|---|---:|
| `native` (XLM) | 10,100 |
| `WGUARDIAN` | **2,077** |
| `AQUA` | 1,308 |
| `USDC` | 885 |
| `yXLM` | 881 |
| `LIBRE` | 875 |

`WGUARDIAN` appears in more than twice as many pools as USDC. Of its 2,077
pools, 2,019 have exactly one trustline, all 2,077 hold non-zero reserves, and
the counterpart assets are almost entirely distinct from one another — the most
frequent counterpart appears six times. One asset is paired against roughly two
thousand different obscure assets, one pool each. This document records the
pattern and does not speculate about intent; what matters here is that it is
large enough to distort any statistic computed over "pools" as an unweighted
population.

### Activity

`last_modified_ledger` moves on every trade as well as every deposit, so this
measures pool activity, not position activity.

| Touched within (approx.) | Pools | Share |
|---|---:|---:|
| ~1 day | 13,045 | 32.75% |
| ~7 days | 20,964 | 52.63% |
| ~30 days | 25,611 | 64.30% |
| ~90 days | 29,894 | 75.05% |
| ~365 days | 33,739 | 84.70% |

Day conversions assume ~5s per ledger and are approximate; the ledger numbers
they derive from are exact.

### Emptiness

- 428 pools (1.07%) hold zero in both reserves, and all 428 also have zero
  `total_shares`. There are **no pools with shares outstanding against empty
  reserves** — no claims on nothing.
- No pool has exactly one empty reserve. Constant-product pools empty
  symmetrically or not at all, which is what the invariant predicts and is worth
  having confirmed rather than assumed.
- 2,224 pools have a share supply below one unit.

### Magnitude

Non-zero reserve amounts span from 1 stroop (`1E-7`) to `922337203678.038574` —
a ratio of **9.2 × 10¹⁸**. The upper figure is just under the protocol's
`922337203685.4775807` ceiling, which is `int64` max at 1e-7 resolution. 248
reserves sit at the one-stroop floor.

---

## What is missing or unusable

**There is no price.** Nothing on-chain denominates a pool in anything. Every
value figure in this document is measured on a single leg — XLM or USDC — which
means pools with neither leg (74.6% of the population by count) cannot be ranked
by size at all with what has been ingested. Cross-pool value comparison requires
an external price source Gauge does not have and has not yet decided to trust.

**`total_trustlines` is not a holder count.** A trustline can exist with a zero
balance. In the attribution run, 204 of the positions read back had a balance of
exactly zero — accounts holding a trustline to a pool share asset with no stake
in it. Every holder-count figure above is therefore an upper bound, and the
survey uses it as one.

**Current state carries no entry basis.** A share balance says what a position
is worth now. It says nothing about what was paid for it, so impermanent loss,
drawdown and net P&L — three of the six metrics this project intends to build —
are not computable from anything in these runs. That requires the effects
history, which is not ingested here.

**Reconstructing entry basis from pools is impractical, and the measurement says
so.** The `liquidity_pool_deposited` effect carries everything needed: the
account, `reserves_deposited` for both assets, `shares_received`, and the pool
state at that moment. The problem is finding it. Walking 3,000 effects backwards
through the largest pool — 15 pages, covering less than one day — yielded 1,205
`liquidity_pool_trade` effects and exactly **two deposits and one withdrawal**.
Liquidity events are roughly 0.1% of that pool's effect stream. Reconstructing
entry basis for its 838 holders by paging its history is not a viable route.

The per-account route is not slightly better, it is a different order of
magnitude. Walking 1,000 effects for one account that deposits into pools gave
141 deposits and 15 withdrawals: **17.0% liquidity events, against 0.1% on the
pool endpoint — roughly 170× denser.** Whatever Phase 2 does about entry basis,
it should read `/accounts/{id}/effects`, not `/liquidity_pools/{id}/effects`.
That is a design decision this survey settled and would otherwise have been
guessed at.

**The holder endpoint is unreliable.** `GET /accounts?liquidity_pool=<id>` is
the only route from Horizon to position data, and it returns HTTP 503 under
load. Recording a single-page fixture for the test suite took four attempts:
three consecutive 503s, then a 200. The client retries; the manifest enumerates
every pool that could not be fetched after retries, by ID, so a census with
holes has labelled holes.

**Horizon has already forgotten a year-old ledger, and this is measurable.**
This started as an open question in an earlier draft and turned out to be cheap
to answer: Horizon reports its own retention boundary at the root endpoint. As
of 2026-09-06T13:13:57Z, `horizon.stellar.org` reports

```
history_latest_ledger: 64,301,173
history_elder_ledger:  57,993,841
```

a retained window of 6,307,332 ledgers — almost exactly **365 days**. Nothing
before it is retrievable from this instance at any price.

The consequences for the population:

| | Pools | Share |
|---|---:|---:|
| Last activity predates the retention window | **6,097** | 15.31% |
| …of those, XLM-legged holding ≥ 1,000 XLM | 3 | — |

For those 6,097 pools there is no recoverable history whatsoever — not the
deposits, not the trades, not even the most recent event. They exist in current
state with nothing behind them. Probing the single oldest-touched pool in the
census directly confirms it: `GET /liquidity_pools/<id>/effects?order=asc`
returns an empty record set.

This cuts the other way for the population Gauge actually cares about. Of the
208 in-scope pools identified below, **207 (99.5%) have activity inside the
retention window**. The unrecoverable pools are almost entirely the dust.

One caveat that the pool-level figure hides, and it is the important one: a pool
being active inside the window does not mean each *position* in it was opened
inside the window. A provider who deposited three years ago into a pool that
trades daily has a live position and no recoverable entry basis. **How many
positions are in that state is not measured here**, and it is the first thing
Phase 2 should establish before promising a realised P&L for anything.

---

## What surprised me

Four things, in ascending order of how much they changed the plan.

### The float ban stopped being a principle and became a measurement

The repository banned floats in the accounting path on the first commit, on the
general argument that money should be exact. That argument is correct but
abstract, and abstract arguments lose to convenience eventually.

Of the 119,499 monetary values in the census — every reserve amount and every
share supply — **4,610 (3.86%) do not survive a `float64` round trip.** They
come back as a different number. The largest share supply in the population,
`873148035084.8922752`, becomes `873148035084.8923`: nineteen significant digits
into a type that holds about sixteen. The reserve range spans 9.2 × 10¹⁸, which
is three orders of magnitude past the 9.0 × 10¹⁵ integer range a `float64`
represents exactly.

Not a rounding concern in the last decimal place. One value in twenty-six,
changed, on the way in.

### Every reconciliation was exact

For pools where the complete holder set was fetched, the holder balances must
sum to the pool's `total_shares`. This was written as a check expected to find
something — a rounding discrepancy, a pagination gap, an assumption that did not
hold.

**12,330 of 12,330 pools reconciled exactly.** Not approximately, not within a
tolerance: `sum(holder balances) == total_shares`, decimal equality, zero
failures. Two independent Horizon endpoints, thousands of accounts, and no
discrepancy anywhere.

This is a null result and it is worth as much as a positive one. It says the
ingestion path is not losing or duplicating anything, that Horizon's two views
of a pool agree, and that decimal arithmetic holds end to end from wire format
to run file. Had a single pool disagreed, every metric built on this data would
have inherited the doubt.

### Diversification has a hard ceiling, and it is the protocol, not preference

The attribution pass reads accounts, not just pools, and the account population
turns out to be sharply bimodal:

| Positions held | Accounts | Share of accounts | Share of all positions |
|---|---:|---:|---:|
| exactly 1 | 10,097 | 66.97% | 13.79% |
| 2–5 | 3,895 | 25.84% | 16.30% |
| 6–20 | 671 | 4.45% | 9.12% |
| 21–100 | 258 | 1.71% | 16.94% |
| 101–500 | 155 | 1.03% | **43.86%** |
| 501+ | **0** | 0.00% | 0.00% |

Two thirds of liquidity providers hold exactly one position and account for
one seventh of all positions. Meanwhile 155 accounts — one percent of the
population — hold 44% of every position on the network, several hundred each.
The top 100 accounts alone hold 34.02% of positions.

The empty bucket is the interesting one. The most diversified account found
holds 426 positions and nobody holds more than 500, which looked like a
behavioural pattern until the accounts were checked directly:

| Account | Pool positions | Other trustlines | `subentry_count` |
|---|---:|---:|---:|
| `GCSO6DAF…` | 426 | 144 | **999** |
| `GB77C7CH…` | 420 | 133 | 995 |
| `GCM3WX7Y…` | 410 | 179 | **999** |
| `GDJEAORW…` | 409 | 169 | **1000** |

A Stellar account may hold at most **1,000 subentries**, and a pool-share
trustline costs two of them (the figures above track `2 × positions + other
trustlines` closely). The most diversified liquidity providers on the network
are not choosing to stop at four hundred pools. **They are full.** One of them
has zero headroom remaining and cannot open another position without closing
one.

This is a structural constraint on the thing Gauge measures, and it is not
visible from pool data at all. Diversification is the standard answer to
position risk; on Stellar it has a hard, countable ceiling of roughly 500
positions per account, and the accounts that matter most are already against it.

### The population worth measuring is about two hundred pools

This is the finding that reframes the project.

There are 39,833 pools. Applying the two filters that any position-risk metric
needs — enough value that the answer means something, and more than one holder
so that a share of the pool is a real question:

| Filter | Pools remaining |
|---|---:|
| All pools | 39,833 |
| ≥ 1,000 XLM on the XLM leg | 316 (0.79%) |
| …and more than one holder | **208 (0.52%)** |

**99.5% of the population is out of scope for the thing this repository was
built to do.** Not because the filters are aggressive — 1,000 XLM is a few
hundred dollars — but because the pool population is dominated by pools that
hold nearly nothing and are held by nearly nobody.

The consequences are not cosmetic:

1. **Any statistic over "all pools" describes pool creation, not liquidity
   provision.** The median pool holds under one XLM. The median holder count is
   one. A model trained on the unweighted population would be modelling the long
   tail of pool spam.
2. **The concentration metric needs restating before it is written.** Its honest
   form is not "how concentrated is this pool among its holders" — a question
   that is degenerate for 86% of pools — but something closer to "how
   concentrated is Stellar AMM liquidity across pools", where the answer is one
   pool holding 54% of pooled XLM. That is a coarser measure than account-level
   attribution and this document is where the reason is recorded.
3. **The scale objection to Phase 2 disappears.** Reconstructing full effect
   history for 39,833 pools is infeasible. For 208 pools and their holders it is
   an afternoon. The expensive plan was expensive because it was aimed at the
   wrong population.

The number that made this session worth running is not 39,833. It is 208.

---

## What remains uncertain

- **The value ranking is single-legged.** Pools without an XLM or USDC leg —
  the majority by count — are unranked, so "the 208" is a floor. A pool holding
  substantial value in two assets Gauge cannot price is invisible to this
  filter and would belong in scope.
- **1,000 XLM is a judgement, not a finding.** It was chosen as a threshold
  below which a position's risk is not economically interesting. A different
  threshold gives a different population, and the shape of the distribution
  (96.87% below it) means the answer is not very sensitive to where the line
  goes — but the line is still a choice.
- **Trustline counts are upper bounds** on holders, by the zero-balance
  positions observed. How much they over-count across the whole population is
  not measured here.
- **The activity figures measure pools, not positions.** A pool trades
  constantly while its liquidity providers do nothing for a year; both look like
  activity in `last_modified_ledger`.
- **Nothing here is longitudinal.** This is one census on one day. Whether these
  distributions are stable, seasonal, or currently anomalous is unknown, and no
  claim in this document should be read as a claim about any other day.
