"""Reading an ingest run from disk.

A run is the unit of reproducibility in Gauge: metrics are computed from
committed bytes, never from a live fetch, so that a figure in a document can be
checked later and two people running the same command get the same answer.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path

from .exact import dec


@dataclass(frozen=True)
class Pool:
    id: str
    fee_bp: int
    total_shares: Decimal
    total_trustlines: int
    asset_a: str
    asset_b: str
    reserve_a: Decimal
    reserve_b: Decimal
    last_modified_ledger: int

    @property
    def is_drained(self) -> bool:
        return self.reserve_a == 0 and self.reserve_b == 0


# Below this, a trade's reported price is too coarsely quantised to be a price.
#
# Stellar stores amounts as integers of 1e-7. A swap of N stroops against M
# stroops reports the rational M/N, whose relative granularity is 1/N — so the
# smaller the trade, the coarser the price it can express, regardless of what
# the pool is actually worth.
#
# At one stroop the rational degenerates completely: one stroop for one stroop
# reports exactly 1, and two for one reports exactly 2. At twelve stroops the
# best it can say is a thirteenth. The XLM/yXLM pool, whose true ratio is about
# 1.003, has a price series dominated by 1, 2, 9/8, 13/12, 3/2 and 4/3 for
# exactly this reason, and it reported a drawdown of -50.00% from a "peak" of 2
# that was a two-stroop swap.
#
# The threshold is therefore set by precision, not by size: 100 stroops bounds
# the quantisation error at 1%, which is finer than any price move these metrics
# are meant to detect. An earlier version used 10 stroops, which caught the
# degenerate 1/1 and 2/1 cases but let the 13/12 family through.
#
# It is still an absurdly small trade — 1e-5 XLM is a few millionths of a cent —
# so nothing with economic content is excluded. What is excluded is only the
# range where the ledger's own resolution prevents the price from being stated.
DUST_THRESHOLD = Decimal("0.00001")


@dataclass(frozen=True)
class Trade:
    """One price observation, normalised to the pool's A-in-B direction."""

    pool_id: str
    id: str
    close_time: str
    price_a_in_b: Decimal
    base_amount: Decimal = Decimal(0)
    counter_amount: Decimal = Decimal(0)

    @property
    def is_dust(self) -> bool:
        """Whether either side is too small for the reported price to mean anything.

        A trade with no recorded amounts (older run files) is not treated as
        dust: absent information must not masquerade as a judgement.
        """
        if self.base_amount == 0 and self.counter_amount == 0:
            return False
        return (
            self.base_amount <= DUST_THRESHOLD or self.counter_amount <= DUST_THRESHOLD
        )


@dataclass(frozen=True)
class Position:
    account_id: str
    pool_id: str
    shares: Decimal
    last_modified_ledger: int


def _pool(row: dict) -> Pool:
    r = row["reserves"]
    return Pool(
        id=row["id"],
        fee_bp=row["fee_bp"],
        # dec() refuses floats; json.loads gives these back as strings because
        # the Go writer emits decimal strings, and that is the contract.
        total_shares=dec(row["total_shares"]),
        total_trustlines=row["total_trustlines"],
        asset_a=r[0]["asset"],
        asset_b=r[1]["asset"],
        reserve_a=dec(r[0]["amount"]),
        reserve_b=dec(r[1]["amount"]),
        last_modified_ledger=row["last_modified_ledger"],
    )


@dataclass
class Run:
    directory: Path
    manifest: dict
    pools: dict[str, Pool]
    positions: list[Position]
    # Pool state as read at the moment holders were fetched. Preferred over
    # `pools` for any share-of-pool arithmetic: the census row may be hours
    # older, and on the run of 2026-09-06 five pools moved in between.
    pools_at_attribution: dict[str, Pool]
    # Price observations per pool, oldest first. Empty when the run did no
    # trade pass; a pool absent from this map has no series, which is different
    # from having a flat one.
    trades: dict[str, list[Trade]]

    def price_series_for(
        self, pool_id: str, *, drop_dust: bool = True
    ) -> list[Decimal]:
        """Prices for a pool, oldest first, dust excluded by default.

        Ordered by close time rather than by the order Horizon returned them:
        the trade walk fetches newest-first and pages backwards, so the raw file
        is in descending time. Feeding that to a drawdown calculation would
        measure the series running backwards, which reports the recovery as the
        decline.

        Dust is excluded because a swap of a few stroops cannot express a price:
        its rational is quantised at 1/N, so a one-stroop swap reports exactly 1
        whatever the pool is worth. Drawdown is a max over the series and so is
        maximally sensitive to one outlier — in the native/LUSD pool, trading
        near 5958, a single one-stroop trade produced a reported peak-to-trough
        of -99.98%. In XLM/yXLM, whose true ratio is about 1.003, 166 of 600
        observations were of this kind and the reported drawdown was -50.00%.

        Pass ``drop_dust=False`` to see the unfiltered series, which is what the
        dust_count comparison in the survey is built from.
        """
        trades = self.trades.get(pool_id, [])
        if drop_dust:
            trades = [t for t in trades if not t.is_dust]
        return [t.price_a_in_b for t in trades]

    def dust_count(self, pool_id: str) -> int:
        """Trades excluded from this pool's series as unpriceable."""
        return sum(1 for t in self.trades.get(pool_id, []) if t.is_dust)

    def pool_for(self, pool_id: str) -> Pool | None:
        """The most contemporaneous pool state available for a position."""
        return self.pools_at_attribution.get(pool_id) or self.pools.get(pool_id)

    @property
    def contemporaneous(self) -> bool:
        """Whether attribution-time pool state exists for this run.

        Runs produced before that fix have only census-era pool rows, so their
        share-of-pool figures carry the skew the reconciliation found. Reported
        rather than silently tolerated.
        """
        return bool(self.pools_at_attribution)


def load(directory: str | Path) -> Run:
    d = Path(directory)
    manifest_path = d / "manifest.json"
    if not manifest_path.exists():
        raise FileNotFoundError(
            f"{d} has no manifest.json — an interrupted run, not a complete one"
        )
    manifest = json.loads(manifest_path.read_text())

    pools: dict[str, Pool] = {}
    for line in (d / "pools.jsonl").read_text().splitlines():
        if line.strip():
            p = _pool(json.loads(line))
            pools[p.id] = p

    at_attr: dict[str, Pool] = {}
    attr_path = d / "pools_at_attribution.jsonl"
    if attr_path.exists():
        for line in attr_path.read_text().splitlines():
            if line.strip():
                p = _pool(json.loads(line))
                at_attr[p.id] = p

    positions: list[Position] = []
    pos_path = d / "positions.jsonl"
    if pos_path.exists():
        for line in pos_path.read_text().splitlines():
            if line.strip():
                row = json.loads(line)
                positions.append(
                    Position(
                        account_id=row["account_id"],
                        pool_id=row["pool_id"],
                        shares=dec(row["shares"]),
                        last_modified_ledger=row["last_modified_ledger"],
                    )
                )

    trades: dict[str, list[Trade]] = {}
    trades_path = d / "trades.jsonl"
    if trades_path.exists():
        for line in trades_path.read_text().splitlines():
            if not line.strip():
                continue
            row = json.loads(line)
            trades.setdefault(row["pool_id"], []).append(
                Trade(
                    pool_id=row["pool_id"],
                    id=row["id"],
                    close_time=row["close_time"],
                    price_a_in_b=dec(row["price_a_in_b"]),
                    # Absent in runs recorded before amounts were carried.
                    base_amount=dec(row.get("base_amount", "0")),
                    counter_amount=dec(row.get("counter_amount", "0")),
                )
            )
    # Oldest first. See price_series_for.
    for series in trades.values():
        series.sort(key=lambda t: t.close_time)

    return Run(d, manifest, pools, positions, at_attr, trades)
