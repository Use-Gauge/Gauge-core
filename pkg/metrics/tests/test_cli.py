"""The CLI's contract, especially the dash.

These fixtures are constructed, not real, and are clearly test scaffolding
rather than data about any position. What they pin is behaviour: that a
quantity the ledger cannot support prints as '-' and never as 0, that a run
without contemporaneous pool state says so, and that the in-scope filter is the
survey's filter.
"""

import json
from decimal import Decimal as D

from gauge_metrics.cli import HISTORY_ELDER_LEDGER, fmt, in_scope, main
from gauge_metrics.run import load


def write_run(tmp_path, *, pools, positions, at_attribution=None, trades=None):
    d = tmp_path / "run"
    d.mkdir(parents=True)
    (d / "manifest.json").write_text(json.dumps({"tool": "test", "pools": len(pools)}))
    (d / "pools.jsonl").write_text("".join(json.dumps(p) + "\n" for p in pools))
    (d / "positions.jsonl").write_text("".join(json.dumps(p) + "\n" for p in positions))
    if at_attribution:
        (d / "pools_at_attribution.jsonl").write_text(
            "".join(json.dumps(p) + "\n" for p in at_attribution)
        )
    if trades:
        (d / "trades.jsonl").write_text("".join(json.dumps(t) + "\n" for t in trades))
    return d


def pool(pid, xlm="5000", other="10000", trustlines=2, shares="1000"):
    return {
        "id": pid,
        "type": "constant_product",
        "fee_bp": 30,
        "total_shares": shares,
        "total_trustlines": trustlines,
        "reserves": [
            {"asset": "native", "amount": xlm},
            {"asset": "AAA:GISSUER", "amount": other},
        ],
        "last_modified_ledger": 64000000,
        "last_modified_time": "2026-09-06T00:00:00Z",
    }


def position(pid, acct, shares, ledger=64000000):
    return {
        "account_id": acct,
        "pool_id": pid,
        "shares": shares,
        "last_modified_ledger": ledger,
    }


def test_unavailable_prints_a_dash_not_zero():
    """The distinction the whole metrics layer is built on."""
    assert fmt(None) == "-"
    assert fmt(D(0)) == "0.0000"
    assert fmt(D(0), pct=True) == "0.0000%"
    assert fmt(D(0), 2, pct=True) == "0.00%"


def test_in_scope_is_the_surveys_filter(tmp_path):
    d = write_run(
        tmp_path,
        pools=[
            pool("big", xlm="5000", trustlines=5),  # in scope
            pool("thin", xlm="999", trustlines=5),  # too little value
            pool("solo", xlm="5000", trustlines=1),  # one holder
        ],
        positions=[],
    )
    assert set(in_scope(load(d))) == {"big"}


def test_no_pool_without_a_native_leg_can_pass_the_value_filter(tmp_path):
    """A real limit of the filter, not a property of the pools.

    XLM is the only denominator the ledger offers, so a pool holding substantial
    value in two assets Gauge cannot price is invisible here. This is why the
    survey calls 208 a floor rather than a count.
    """
    p = pool("rich", trustlines=9)
    p["reserves"] = [
        {"asset": "AAA:GX", "amount": "100000000"},
        {"asset": "BBB:GY", "amount": "100000000"},
    ]
    d = write_run(tmp_path, pools=[p], positions=[])
    assert in_scope(load(d)) == []


def test_run_without_contemporaneous_state_warns(tmp_path, capsys):
    d = write_run(
        tmp_path,
        pools=[pool("p", trustlines=2)],
        positions=[position("p", "A", "600"), position("p", "B", "400")],
    )
    main([str(d), "--limit", "5"])
    out = capsys.readouterr().out
    assert "WARNING" in out
    assert "pools_at_attribution" in out


def test_contemporaneous_run_does_not_warn(tmp_path, capsys):
    pools = [pool("p", trustlines=2)]
    d = write_run(
        tmp_path,
        pools=pools,
        positions=[position("p", "A", "600"), position("p", "B", "400")],
        at_attribution=pools,
    )
    main([str(d), "--limit", "5"])
    assert "WARNING" not in capsys.readouterr().out


def test_positions_predating_retention_report_no_entry_basis(tmp_path, capsys):
    pools = [pool("p", trustlines=2)]
    d = write_run(
        tmp_path,
        pools=pools,
        positions=[
            position("p", "OLD", "600", ledger=HISTORY_ELDER_LEDGER - 1),
            position("p", "NEW", "400", ledger=HISTORY_ELDER_LEDGER + 1),
        ],
        at_attribution=pools,
    )
    main([str(d), "--limit", "5"])
    out = capsys.readouterr().out
    assert "1 of 2 positions moved inside the retention window" in out


def test_hhi_appears_only_with_a_complete_holder_set(tmp_path, capsys):
    # Two trustlines declared, two positions present: complete.
    pools = [pool("p", trustlines=2)]
    d = write_run(
        tmp_path,
        pools=pools,
        positions=[position("p", "A", "600"), position("p", "B", "400")],
        at_attribution=pools,
    )
    main([str(d), "--limit", "5"])
    assert "0.5200" in capsys.readouterr().out  # 0.6^2 + 0.4^2

    # Three declared, two present: a subset, so no HHI.
    pools = [pool("q", trustlines=3)]
    d2 = write_run(
        tmp_path / "second",
        pools=pools,
        positions=[position("q", "A", "600"), position("q", "B", "400")],
        at_attribution=pools,
    )
    main([str(d2), "--limit", "5"])
    out = capsys.readouterr().out
    assert "0.5200" not in out


def test_series_metrics_appear_when_trades_exist(tmp_path, capsys):
    pools = [pool("p", trustlines=2)]
    trades = [
        {
            "pool_id": "p",
            "id": f"t{i}",
            "close_time": f"2026-09-06T00:0{i}:00Z",
            "price_a_in_b": price,
        }
        for i, price in enumerate(["2.0", "2.2", "1.8", "2.1", "1.9"])
    ]
    d = write_run(
        tmp_path,
        pools=pools,
        positions=[position("p", "A", "600"), position("p", "B", "400")],
        at_attribution=pools,
        trades=trades,
    )
    main([str(d), "--limit", "5"])
    out = capsys.readouterr().out
    # Peak 2.2 -> trough 1.8 is -18.18%.
    assert "-18.18%" in out


def test_trades_are_ordered_oldest_first_regardless_of_file_order(tmp_path):
    """The walk pages newest-first. A drawdown over a reversed series reports
    the recovery as the decline."""
    pools = [pool("p", trustlines=2)]
    trades = [
        {
            "pool_id": "p",
            "id": "t2",
            "close_time": "2026-09-06T00:02:00Z",
            "price_a_in_b": "1.0",
        },
        {
            "pool_id": "p",
            "id": "t1",
            "close_time": "2026-09-06T00:01:00Z",
            "price_a_in_b": "2.0",
        },
    ]
    d = write_run(
        tmp_path, pools=pools, positions=[], at_attribution=pools, trades=trades
    )
    assert load(d).price_series_for("p") == [D("2.0"), D("1.0")]
