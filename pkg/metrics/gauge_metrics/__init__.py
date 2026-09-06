"""Gauge's deterministic metrics layer.

Every formula here is written out in source with its derivation, verified by
hand against known cases in ``docs/metrics-verification.md``, and computed in
exact decimal arithmetic except where ``series.py`` explicitly crosses into the
statistical layer.

Nothing in this package trains, fits, or predicts.
"""

__all__ = [
    "concentration",
    "exact",
    "impermanent_loss",
    "position",
    "run",
    "series",
]
