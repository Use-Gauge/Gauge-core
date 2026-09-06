"""``gauge-metrics`` — a table of deterministic metrics per position.

    python -m gauge_metrics <run-directory> [--limit N] [--all-pools]

Reads a committed ingest run and reports, for each position in the in-scope
population, what the ledger supports computing. Quantities the ledger does not
support are printed as ``-`` and never estimated.
"""

from __future__ import annotations

import argparse
import sys
from decimal import Decimal

from .concentration import holder_hhi, top_holder_share
from .exact import ZERO, quantise
from .position import claim, net_pnl
from .run import Run, load
from .series import max_drawdown, realised_volatility

# Horizon's retention boundary on horizon.stellar.org, observed 2026-09-06.
# A position that last changed before this has no recoverable entry basis.
HISTORY_ELDER_LEDGER = 57993841

# In-scope threshold from docs/data-survey.md: enough value that the answer
# means something. A judgement, not a finding, and stated as one.
MIN_XLM = Decimal("1000")


def in_scope(run: Run) -> list[str]:
    """Pool IDs meeting the survey's in-scope criteria."""
    out = []
    for p in run.pools.values():
        if p.total_trustlines <= 1:
            continue
        for asset, reserve in ((p.asset_a, p.reserve_a), (p.asset_b, p.reserve_b)):
            if asset == "native" and reserve >= MIN_XLM:
                out.append(p.id)
                break
    return out


def short(s: str, n: int = 10) -> str:
    return s[:n] + "…" if len(s) > n else s


def asset_name(a: str) -> str:
    return "XLM" if a == "native" else a.split(":")[0]


def fmt(value: Decimal | float | None, places: int = 4, pct: bool = False) -> str:
    """Format a metric, or ``-`` when the ledger does not support it.

    The dash is load-bearing. It is the difference between "this position broke
    even" and "nobody can know what this position did", and the survey found the
    second case is 82% of the population.
    """
    if value is None:
        return "-"
    if isinstance(value, float):  # statistical layer only
        return f"{value:.{places}f}"
    if pct:
        return f"{quantise(value * 100, places)}%"
    return str(quantise(value, places))


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="gauge-metrics", description=__doc__)
    ap.add_argument("run", help="ingest run directory")
    ap.add_argument("--limit", type=int, default=25, help="rows to print (0 = all)")
    ap.add_argument(
        "--all-pools",
        action="store_true",
        help="every pool with positions, not just the in-scope population",
    )
    args = ap.parse_args(argv)

    run = load(args.run)

    scope = set(run.pools) if args.all_pools else set(in_scope(run))

    by_pool: dict[str, list] = {}
    for pos in run.positions:
        if pos.pool_id in scope:
            by_pool.setdefault(pos.pool_id, []).append(pos)

    print(f"run:      {run.directory}")
    print(f"pools:    {len(run.pools):,}   positions: {len(run.positions):,}")
    print(
        f"scope:    {'all pools' if args.all_pools else 'in-scope'} "
        f"({len(by_pool):,} pools with positions)"
    )
    if not run.contemporaneous:
        print(
            "WARNING:  this run has no pools_at_attribution.jsonl, so pool state "
            "may be\n          hours older than the holder balances. Share-of-pool "
            "figures carry\n          that skew. Re-run ingest to get "
            "contemporaneous state."
        )
    print(
        f"note:     entry basis is unrecoverable for positions last changed "
        f"before\n          ledger {HISTORY_ELDER_LEDGER:,}; those show '-' for "
        "IL, fee yield and P&L."
    )
    tstats = run.manifest.get("trades")
    if tstats:
        print(
            f"prices:   {tstats['trades']:,} trades across {tstats['pools']:,} pools, "
            f"{tstats['earliest_close_time']} .. {tstats['latest_close_time']}\n"
            "          vol is unannualised stdev of log returns over that window; "
            "maxDD is\n          peak-to-trough over the same."
        )
    else:
        print(
            "prices:   no trade history in this run, so vol and maxDD are '-'. "
            "A census\n          gives one price per pool, and one point has no "
            "variance. Re-run\n          ingest with -trades N."
        )
    print()

    header = (
        f"{'account':<11} {'pool':<11} {'pair':<18} {'share%':>9} "
        f"{'claim A':>16} {'claim B':>16} {'HHI':>7} {'top%':>7} "
        f"{'IL':>9} {'feeYld':>9} {'net%':>9} {'vol':>8} {'maxDD':>9}"
    )
    print(header)
    print("-" * len(header))

    rows = 0
    stats = {"with_entry": 0, "no_entry": 0}

    for pool_id in sorted(by_pool):
        pool = run.pool_for(pool_id)
        if pool is None or pool.is_drained:
            continue
        positions = by_pool[pool_id]

        # Series metrics are per-pool, not per-position: every holder of a pool
        # experiences the same price path. Computed once here rather than per
        # row, which also keeps the table honest about what varies.
        series = run.price_series_for(pool_id)
        vol = realised_volatility(series)
        dd = max_drawdown(series).max_drawdown

        # HHI needs the complete holder set. It is complete only when the number
        # of positions found equals the pool's trustline count.
        complete = len(positions) == pool.total_trustlines
        balances = [p.shares for p in positions]
        hhi = holder_hhi(balances, complete=complete)
        top = top_holder_share(balances, complete=complete)

        for pos in sorted(positions, key=lambda p: -p.shares):
            if pos.shares <= ZERO:
                continue

            c = claim(pos.shares, pool.total_shares, pool.reserve_a, pool.reserve_b)
            if c is None:
                continue

            # Entry basis: available only if this position's balance last moved
            # inside Horizon's retention window. Even then the establishing
            # deposit may predate it, so this is an upper bound and the effects
            # walk in Phase 2's next step is what confirms it.
            recoverable = pos.last_modified_ledger >= HISTORY_ELDER_LEDGER
            if recoverable:
                stats["with_entry"] += 1
            else:
                stats["no_entry"] += 1

            pnl = net_pnl(
                shares=pos.shares,
                total_shares=pool.total_shares,
                reserve_a=pool.reserve_a,
                reserve_b=pool.reserve_b,
            )

            print(
                f"{short(pos.account_id):<11} {short(pool.id):<11} "
                f"{asset_name(pool.asset_a) + '/' + asset_name(pool.asset_b):<18} "
                f"{fmt(c.pool_fraction * 100, 4):>9} "
                f"{fmt(c.reserve_a, 4):>16} {fmt(c.reserve_b, 4):>16} "
                f"{fmt(hhi, 4):>7} {fmt(top, 4):>7} "
                f"{fmt(pnl.impermanent_loss if pnl else None, 2, pct=True):>9} "
                f"{fmt(pnl.fee_yield if pnl else None, 2, pct=True):>9} "
                f"{fmt(pnl.net_fraction if pnl else None, 2, pct=True):>9} "
                f"{fmt(vol, 4):>8} {fmt(dd, 2, pct=True):>9}"
            )
            rows += 1
            if args.limit and rows >= args.limit:
                break
        if args.limit and rows >= args.limit:
            break

    total = stats["with_entry"] + stats["no_entry"]
    if total:
        pct_no = Decimal(stats["no_entry"]) * 100 / total
        print()
        print(
            f"entry basis: {stats['with_entry']:,} of {total:,} positions moved "
            f"inside the retention window;\n"
            f"             {stats['no_entry']:,} ({quantise(pct_no, 2)}%) did not "
            "and can never have one."
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
