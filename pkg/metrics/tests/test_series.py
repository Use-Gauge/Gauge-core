"""Drawdown stays exact; volatility is the one place floats are allowed."""

from decimal import Decimal as D

from gauge_metrics.series import max_drawdown, price_series, realised_volatility


def dd(*xs):
    return max_drawdown([D(x) for x in xs]).max_drawdown


def test_simple_peak_to_trough():
    assert dd("100", "120", "60", "80") == D("-0.5")


def test_trough_before_peak_is_not_a_drawdown():
    """The trap: min/max over the whole series would report -75% here, a decline
    that never happened. The peak must be tracked forward in time."""
    assert dd("100", "50", "200") == D("-0.5")


def test_monotonic_rise_is_zero_not_none():
    assert dd("1", "2", "3") == D(0)


def test_too_short_is_none():
    assert dd("1") is None
    assert max_drawdown([]).max_drawdown is None


def test_drawdown_stays_exact():
    """It is a ratio of two observed values and needs no transcendental, so it
    must not have gone through a float."""
    result = dd("3", "1")
    assert isinstance(result, D)
    assert result == D(1) / D(3) - D(1)


def test_volatility_returns_a_float_deliberately():
    """The type is the documentation: a statistic, never formatted as money."""
    v = realised_volatility([D(x) for x in ["100", "101", "99", "102", "98"]])
    assert isinstance(v, float)
    assert v > 0


def test_volatility_of_a_flat_series_is_zero():
    assert realised_volatility([D(100)] * 6) == 0.0


def test_volatility_needs_enough_observations():
    """Two points give one return and a sample stdev of zero, which would report
    a volatile pool as perfectly calm."""
    assert realised_volatility([D(100), D(200)]) is None
    assert realised_volatility([D(100)]) is None


def test_annualisation_scales_by_root_time():
    series = [D(x) for x in ["100", "101", "99", "102", "98", "103"]]
    base = realised_volatility(series)
    annual = realised_volatility(series, ledgers_between=17280)  # ~1 day
    assert annual > base
    assert abs(annual / base - (365**0.5)) < 0.001


def test_price_series_drops_drained_observations():
    s = price_series([(D(100), D(200)), (D(0), D(0)), (D(50), D(200))])
    assert s == [D(2), D(4)]
