"""Do positions that moved recently actually have a recoverable entry basis?

Horizon retains 365 days of history (`history_elder_ledger` 57,993,841 as of
2026-09-06). A position whose balance last changed before that boundary has no
recoverable cost basis at all — the history is gone.

The positions that moved *after* it are an upper bound on what is recoverable,
not a count: a balance that moved recently can belong to a position opened years
ago and merely topped up since.

This script distinguishes the two. For a sampled position it sums the account's
in-window `liquidity_pool_deposited` and `liquidity_pool_withdrew` effects for
that pool. If the net equals the current balance, the position began at zero
inside the window and its entry basis is complete. If it does not, part of the
position predates the window and only a partial history exists.

    python3 scripts/entry-basis-sample.py <run-directory> [sample-size]

The sample is seeded, so a rerun over the same run examines the same positions.
"""

from __future__ import annotations

import json
import random
import sys
import urllib.request
from decimal import Decimal

# Horizon's retention boundary, observed on horizon.stellar.org 2026-09-06.
ELDER_LEDGER = 57993841

# Minimum XLM on a native leg for a pool to be in scope. The survey's threshold;
# a judgement, not a finding.
MIN_XLM = Decimal(1000)

# How deep to walk an account's effects before giving up. A cap is necessary —
# some accounts have very long histories — and positions unresolved within it
# are reported as inconclusive rather than counted either way.
MAX_PAGES = 25

SEED = 20260906


def fetch(url: str, attempts: int = 4) -> dict | None:
    """GET with retries. The accounts endpoint returns 503 under load."""
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(url, timeout=30) as response:
                return json.load(response)
        except Exception:
            continue
    return None


def in_scope_pools(run_dir: str) -> set[str]:
    scope = set()
    with open(f"{run_dir}/pools.jsonl") as handle:
        for line in handle:
            pool = json.loads(line)
            if pool["total_trustlines"] <= 1:
                continue
            for reserve in pool["reserves"]:
                if reserve["asset"] == "native" and Decimal(reserve["amount"]) >= MIN_XLM:
                    scope.add(pool["id"])
                    break
    return scope


def candidates(run_dir: str, scope: set[str]) -> list[dict]:
    """Positions in scope, non-zero, whose balance moved inside the window."""
    out = []
    with open(f"{run_dir}/positions.jsonl") as handle:
        for line in handle:
            position = json.loads(line)
            if position["pool_id"] not in scope:
                continue
            if Decimal(position["shares"]) <= 0:
                continue
            if position["last_modified_ledger"] < ELDER_LEDGER:
                continue
            out.append(position)
    return out


def net_in_window(account: str, pool_id: str) -> tuple[Decimal, int]:
    """Net shares deposited minus redeemed for this pool, within the window."""
    url = (
        f"https://horizon.stellar.org/accounts/{account}"
        f"/effects?limit=200&order=desc"
    )
    net = Decimal(0)
    events = 0
    for _ in range(MAX_PAGES):
        page = fetch(url)
        if page is None:
            break
        records = page["_embedded"]["records"]
        if not records:
            break
        for record in records:
            pool = record.get("liquidity_pool") or {}
            if pool.get("id") != pool_id:
                continue
            if record["type"] == "liquidity_pool_deposited":
                net += Decimal(record.get("shares_received", "0"))
                events += 1
            elif record["type"] == "liquidity_pool_withdrew":
                net -= Decimal(record.get("shares_redeemed", "0"))
                events += 1
        url = page["_links"]["next"]["href"]
    return net, events


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__)
        return 2
    run_dir = argv[0]
    sample_size = int(argv[1]) if len(argv) > 1 else 25

    scope = in_scope_pools(run_dir)
    pool_of_interest = candidates(run_dir, scope)
    if not pool_of_interest:
        print("no candidate positions in this run")
        return 1

    random.seed(SEED)
    sample = random.sample(pool_of_interest, min(sample_size, len(pool_of_interest)))
    print(f"candidates: {len(pool_of_interest)}   sampling: {len(sample)}\n")

    complete = partial = inconclusive = 0
    for index, position in enumerate(sample, 1):
        account = position["account_id"]
        pool_id = position["pool_id"]
        balance = Decimal(position["shares"])

        net, events = net_in_window(account, pool_id)
        if events == 0:
            inconclusive += 1
            verdict = f"inconclusive (no events within {MAX_PAGES} pages)"
        elif net == balance:
            complete += 1
            verdict = "COMPLETE (opened in-window)"
        else:
            partial += 1
            verdict = f"partial (net {net} vs balance {balance})"
        print(f"  {index:>3}. {account[:10]}… {pool_id[:10]}… events={events:<3} {verdict}")

    total = len(sample)
    print(f"\n=== result over {total} sampled in-window positions ===")
    for label, count in (
        ("complete (entry basis recoverable)", complete),
        ("partial (predates the window)", partial),
        ("inconclusive (walk limit reached)", inconclusive),
    ):
        print(f"  {label:<38} {count:>3} ({Decimal(count) * 100 / total:.1f}%)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
