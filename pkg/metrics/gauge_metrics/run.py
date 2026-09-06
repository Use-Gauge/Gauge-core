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


# Amounts at or below this are at the ledger's resolution floor, where a
# reported price stops carrying information.
#
# Stellar stores amounts as integers of 1e-7. A swap of one stroop for one
# stroop reports price 1/1 = 1 no matter what the pool is actually worth, and a
# swap of one stroop for two reports 2. These are quantisation artefacts, not
# prices.
#
# The threshold is deliberately at the floor itself rather than at some round
# number above it: excluding a genuinely small but well-formed trade would be
# discarding real data to tidy a chart. What is excluded here is only the range
# where the rational cannot express the price.
DUST_THRESHOLD = Decimal("0.0000010")


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
        """Whether either side is small enough that the price is meaningless.

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

        Dust is excluded because a stroop-for-stroop swap reports a price of
        exactly 1 regardless of the pool's real price. Drawdown is maximally
        sensitive to a single outlier, so one such trade is enough to ruin it:
        in the native/LUSD pool, which trades near 5958, a single one-stroop
        trade produced a reported peak-to-trough of -99.98%.

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
