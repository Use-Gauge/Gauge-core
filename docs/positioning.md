# Positioning: Gauge against LedgerLens

**Status:** written before any Gauge code existed, deliberately. If the
distinction below had not survived contact with LedgerLens's actual source, the
correct outcome was a re-scope, not a variant. It survived, and this document
records the evidence rather than the intuition.

**Investigated:** 2026-09-06, against the `Ledger-Lenz` GitHub organisation.

---

## What LedgerLens is, from its own source

LedgerLens is real, active, and substantially built. Four public repositories,
created 2026-06-12, all pushed within the last week of this investigation:

| Repo | Language | Last push (as of 2026-09-06) |
|---|---|---|
| [`Ledgerlens-core`](https://github.com/Ledger-Lenz/Ledgerlens-core) | Python | 2026-09-01 |
| [`Ledgerlens-contract`](https://github.com/Ledger-Lenz/Ledgerlens-contract) | Rust (Soroban) | 2026-09-02 |
| [`Ledgerlens-data`](https://github.com/Ledger-Lenz/Ledgerlens-data) | Python | 2026-09-03 |
| [`Ledgerlens-dashboard`](https://github.com/Ledger-Lenz/Ledgerlens-dashboard) | JavaScript | 2026-08-11 |

Its own README describes it as:

> "Hybrid on-chain fraud detection for the Stellar DEX — detecting wash trading
> and artificial volume using Benford's Law combined with ensemble machine
> learning, with risk scores anchored on Soroban."

This is not a distant neighbour. `Ledgerlens-data` contains
`ingestion/amm_pool_loader.py` and `detection/liquidity_profiler.py`. It calls
Horizon. It reads AMM pools. **The overlap is closer than a casual reading would
suggest, and pretending otherwise would be the easiest way to build something
redundant.**

So the distinction has to be drawn precisely, or not claimed at all.

---

## Three distinctions that actually hold

### 1. Different unit of analysis on the same pools

`amm_pool_loader.py` ingests from Horizon's pool **trades** endpoint. Its
docstring: *"AMM liquidity pool trade ingestion via Horizon's paginated and SSE
endpoints."* Every record it builds is a `Trade`, carrying `base_account`,
`counter_account`, price and amount.

That is the **swapper** side of a pool — someone trading *against* the AMM.

Gauge's unit is the **liquidity position**: the pool-share balance held by an
account, read from `GET /accounts?liquidity_pool=<id>` as a
`liquidity_pool_shares` balance. That is the **provider** side — someone whose
capital *is* the AMM.

Both projects read the same pools. Neither reads the other's rows. A wash-trade
detector has no reason to enumerate share balances, and a position-risk engine
has no reason to reconstruct counterparty round-trips.

### 2. Liquidity is LedgerLens's nuisance variable and Gauge's subject

`detection/liquidity_profiler.py` is the closest file in their tree to Gauge's
territory. Its docstring is explicit about why it exists:

> "Clusters assets into liquidity regimes … All downstream Benford metrics are
> expressed as deviations from the regime baseline rather than from the
> theoretical Benford distribution, **reducing false positives on legitimate
> market-makers** whose structural behaviour mimics wash-trader digit patterns."

Liquidity is modelled there so it can be **controlled away** — it is a
confounder that makes honest market-makers look guilty. In Gauge, the economics
of that same liquidity is the entire output. The profiler measures liquidity to
stop it contaminating a fraud score; Gauge measures it because the number *is*
the answer.

### 3. The target variable — the sharpest difference, and it is documented in their own repo

The brief's hypothesis was that LedgerLens needs labelled fraud while Gauge's
target is directly observable. That hypothesis is confirmed from primary source,
not asserted.

`Ledgerlens-data/data/labelling_notes.md` describes a conservative two-signal
labelling rule (round-trip detection within ≤100 ledgers at ±5% amounts, plus
funding-ancestor Jaccard similarity > 0.7), with a third manual-review signal.
Wallets flagged by only one signal are labelled `NaN` and excluded. It is
careful, well-reasoned work.

It also says this, in its own words:

> "**Note:** This dataset version uses the synthetic dataset as a reference
> schema baseline. A production release against live Horizon data would populate
> this table with real observations."

Its manual review table contains exactly one row: wallet `GSYNTH*`, decision
"Synthetic".

This is not a criticism — they flagged the limitation themselves, honestly, in
the repo, which is more than most projects do. It is a structural fact about the
problem: **"is this wallet washing trades" has no ground truth on a public
ledger.** Nobody labels their own fraud. The label must be manufactured, whether
by heuristic rule, by simulation (`scripts/wash_trade_simulator.py`), or by
manual review that does not scale. Every downstream number inherits the
uncertainty of that manufactured label.

Gauge's target variable has the opposite property. "Did this position lose
money" is **arithmetic on ledger state**:

- Entry basis comes from `liquidity_pool_deposited` effects — reserves in,
  shares out, both exact decimal amounts.
- Current claim comes from share balance ÷ total shares × current reserves.
- Exit comes from `liquidity_pool_withdrew` effects.
- Fees accrue observably as the reserve-per-share ratio drifts upward.

No labeller is involved. No rule decides what counts. The ledger already
recorded the outcome; Gauge computes it. Where a number cannot be computed —
positions whose deposit history predates the ingested window have no entry basis
— the honest answer is to report it as missing, which is a state a manufactured
label does not have available to it.

---

## The one-line version

**LedgerLens asks whether behaviour is fraudulent. Gauge asks whether capital
lost money.** The first is a classification problem with no ground truth, solved
with careful proxies. The second is an accounting problem with exact answers,
where the hard part is not the label but the arithmetic and the population.

They are complementary, and legitimately so: a pool can be perfectly honest and
still ruinous to provide liquidity to, and a pool being manipulated tells a
provider nothing about the size of their loss.

---

## What follows from this, as constraints on Gauge

1. **Do not build a classifier.** The moment Gauge trains something to predict a
   manufactured label, the distinction above collapses and Gauge becomes a
   weaker LedgerLens. Determinism is the differentiator, not a phase to grow out
   of.
2. **Exact arithmetic in the accounting path.** LedgerLens uses pandas/NumPy
   floats throughout, which is entirely correct for a classification score —
   float error is orders of magnitude below label noise there. It would not be
   correct here: when the output claims to be a P&L, `decimal.Decimal` is the
   only defensible choice, and floats are confined to the eventual statistical
   layer.
3. **Survey before metrics.** The population characteristics of LP positions are
   not documented anywhere I could find, including in LedgerLens, whose
   interest in pools stops at their trades. That makes `docs/data-survey.md`
   Gauge's first genuinely novel artefact, and it comes before any formula.

---

## What remains uncertain

- Read from **public source only**, at the commits live on 2026-09-06. I have
  not run LedgerLens, seen its deployed behaviour, or spoken to its authors. A
  private branch could contradict any of the above.
- `labelling_notes.md` may since have been superseded — it describes a state the
  authors explicitly frame as pre-production, and they may already have
  populated it with real observations.
- A LumenLoop ecosystem-directory lookup for "LedgerLens" on 2026-09-05 returned
  **no exact substring match** (semantic fallback surfaced Lumenscan, LedgersTax
  and Stellarscam.report). The project was located through repository search
  instead. Its directory absence says nothing about the project; it does mean
  this comparison rests on source code rather than on any curated description.
