"""
LiteOps — Production operations framework for LiteRT edge AI deployments.

Phase 1: Observer — real memory metrics, latency histograms, token counters.
"""

from liteops._version import __version__
from liteops.observer import LiteRTObserver, Snapshot

__all__ = ["LiteRTObserver", "Snapshot", "__version__"]
