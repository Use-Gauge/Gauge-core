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


@dataclass(frozen=True)
class Trade:
    """One price observation, normalised to the pool's A-in-B direction."""

    pool_id: str
    id: str
    close_time: str
    price_a_in_b: Decimal


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

    def price_series_for(self, pool_id: str) -> list[Decimal]:
        """Prices for a pool, oldest first.

        Ordered by close time rather than by the order Horizon returned them:
        the trade walk fetches newest-first and pages backwards, so the raw file
        is in descending time. Feeding that to a drawdown calculation would
        measure the series running backwards, which reports the recovery as the
        decline.
        """
        return [t.price_a_in_b for t in self.trades.get(pool_id, [])]

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
                )
            )
    # Oldest first. See price_series_for.
    for series in trades.values():
        series.sort(key=lambda t: t.close_time)

    return Run(d, manifest, pools, positions, at_attr, trades)
