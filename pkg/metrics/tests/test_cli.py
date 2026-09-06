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
from gauge_metrics.series import max_drawdown


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


def trade(pid, tid, minute, price, base="10", counter="10"):
    return {
        "pool_id": pid,
        "id": tid,
        "close_time": f"2026-09-06T00:{minute:02d}:00Z",
        "price_a_in_b": price,
        "base_amount": base,
        "counter_amount": counter,
    }


def test_a_single_dust_trade_does_not_destroy_the_drawdown(tmp_path):
    """The native/LUSD case, reduced.

    A swap of one stroop for one stroop reports a price of exactly 1 whatever
    the pool is worth, because both sides are at the ledger's 1e-7 floor and the
    rational degenerates. Drawdown is maximally sensitive to a single outlier,
    so one such trade took a pool trading near 5958 to a reported -99.98%.
    """
    pools = [pool("p", trustlines=2)]
    trades = [
        trade("p", "t1", 1, "5986.7"),
        trade("p", "t2", 2, "6019.7"),
        # One stroop for one stroop: price 1, and meaningless.
        trade("p", "t3", 3, "1", base="0.0000001", counter="0.0000001"),
        trade("p", "t4", 4, "5938.3"),
    ]
    d = write_run(
        tmp_path, pools=pools, positions=[], at_attribution=pools, trades=trades
    )
    run = load(d)

    assert run.dust_count("p") == 1

    clean = run.price_series_for("p")
    assert D("1") not in clean
    assert len(clean) == 3
    # Real peak-to-trough over the surviving observations, not -99.98%.
    assert max_drawdown(clean).max_drawdown > D("-0.02")

    raw = run.price_series_for("p", drop_dust=False)
    assert len(raw) == 4
    assert max_drawdown(raw).max_drawdown < D("-0.99")


def test_the_thirteen_twelfths_family_is_excluded(tmp_path):
    """The case a 10-stroop threshold let through.

    Twelve stroops against thirteen reports 13/12 = 1.0833…, which looks like a
    plausible price and is not one — it is the finest thing a twelve-stroop
    trade can say. The XLM/yXLM series was full of these.
    """
    pools = [pool("p", trustlines=2)]
    trades = [
        trade("p", "t1", 1, "1.003"),
        trade(
            "p", "t2", 2, "1.0833333333333333", base="0.0000012", counter="0.0000013"
        ),
        trade("p", "t3", 3, "1.004"),
    ]
    d = write_run(
        tmp_path, pools=pools, positions=[], at_attribution=pools, trades=trades
    )
    run = load(d)
    assert run.dust_count("p") == 1
    assert len(run.price_series_for("p")) == 2


def test_a_small_but_well_formed_trade_is_kept(tmp_path):
    """Only the range where the rational cannot express a price is excluded.

    Discarding genuinely small trades would be throwing away real data to tidy
    a chart. 0.001 is 10,000 stroops: quantisation error of one part in ten
    thousand, far finer than any move these metrics detect.
    """
    pools = [pool("p", trustlines=2)]
    trades = [
        trade("p", "t1", 1, "100"),
        trade("p", "t2", 2, "101", base="0.001", counter="0.101"),
        trade("p", "t3", 3, "99"),
    ]
    d = write_run(
        tmp_path, pools=pools, positions=[], at_attribution=pools, trades=trades
    )
    run = load(d)
    assert run.dust_count("p") == 0
    assert len(run.price_series_for("p")) == 3


def test_trades_without_amounts_are_not_treated_as_dust(tmp_path):
    """Runs recorded before amounts were carried have none. Absent information
    must not masquerade as a judgement."""
    pools = [pool("p", trustlines=2)]
    trades = [
        {
            "pool_id": "p",
            "id": "t1",
            "close_time": "2026-09-06T00:01:00Z",
            "price_a_in_b": "100",
        },
        {
            "pool_id": "p",
            "id": "t2",
            "close_time": "2026-09-06T00:02:00Z",
            "price_a_in_b": "90",
        },
    ]
    d = write_run(
        tmp_path, pools=pools, positions=[], at_attribution=pools, trades=trades
    )
    run = load(d)
    assert run.dust_count("p") == 0
    assert len(run.price_series_for("p")) == 2
