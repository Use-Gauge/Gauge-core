"""Concentration, and the three cases where it must decline to answer."""

from decimal import Decimal as D

from gauge_metrics.concentration import holder_hhi, top_holder_share


def hhi(*xs, complete=True):
    return holder_hhi([D(x) for x in xs], complete=complete)


def test_equal_holders_give_one_over_n():
    assert hhi("50", "50") == D("0.5")
    assert hhi("25", "25", "25", "25") == D("0.25")
    assert hhi("10", "10", "10", "10", "10") == D("0.2")


def test_known_split():
    # 0.9^2 + 0.1^2 = 0.81 + 0.01 = 0.82
    assert hhi("90", "10") == D("0.82")


def test_single_holder_is_none_not_one():
    """86.37% of pools are single-holder. Reporting HHI 1.0 for them would be
    measuring the arithmetic, not the market."""
    assert hhi("100") is None
    assert hhi("100", "0") is None  # the zero holder is not a holder


def test_incomplete_holder_set_is_none():
    """A concentration over an unknown fraction of a pool is wrong, not weak."""
    assert hhi("50", "30", complete=False) is None
    assert top_holder_share([D("50")], complete=False) is None


def test_zero_balances_are_excluded():
    """6.58% of positions are trustlines with no stake. Counting them would
    deflate HHI with holders who hold nothing."""
    assert hhi("50", "50", "0", "0") == D("0.5")


def test_scale_invariant():
    """HHI depends on proportions, not units."""
    assert hhi("1", "3") == hhi("1000000", "3000000")


def test_top_holder_share_is_separate_from_hhi():
    """A long tail lowers HHI but not the single point of failure."""
    concentrated = [D("95")] + [D("0.125")] * 40
    assert top_holder_share(concentrated, complete=True) > D("0.94")
    # HHI reads far lower than the 95% holder alone would suggest is safe.
    assert holder_hhi(concentrated, complete=True) < D("0.91")
