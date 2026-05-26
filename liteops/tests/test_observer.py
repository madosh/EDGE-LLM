"""
Phase 1 tests — LiteRTObserver

Tests cover:
- Normal generate() call captures real memory and returns enriched response.
- Memory values appear in _liteops metadata, never 0.0 when process is found.
- Peak memory only increases, never decreases across calls.
- Process auto-discovery from cmdline search.
- Graceful fallback (mem_rss_mb == 0.0) when no lit serve process is found.
- Error path: HTTP failure propagates without recording an "ok" metric.
- snapshot() returns current RSS without calling lit serve.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import psutil
import pytest

from liteops.observer import LiteRTObserver, Snapshot, _read_rss_mb


# ── Fixtures ──────────────────────────────────────────────────────────────────


def _make_proc(rss_bytes: int = 500 * 1_048_576) -> MagicMock:
    """Return a mock psutil.Process with a fixed RSS."""
    proc = MagicMock(spec=psutil.Process)
    proc.memory_info.return_value = MagicMock(rss=rss_bytes)
    return proc


def _fake_litert_response(output_tokens: int = 42) -> dict:
    return {
        "candidates": [{"content": {"parts": [{"text": "Edge AI is fast inference on device."}]}}],
        "usageMetadata": {
            "promptTokenCount": 5,
            "candidatesTokenCount": output_tokens,
            "totalTokenCount": 5 + output_tokens,
        },
    }


@pytest.fixture
def observer() -> LiteRTObserver:
    return LiteRTObserver(
        litert_url="http://localhost:8000",
        device_id="test-hub-01",
        litert_pid=99999,
    )


# ── Normal path ───────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_generate_returns_liteops_metadata(observer: LiteRTObserver) -> None:
    proc = _make_proc(rss_bytes=400 * 1_048_576)  # 400 MB

    with (
        patch("psutil.Process", return_value=proc),
        patch.object(observer, "_call_litert", new=AsyncMock(return_value=_fake_litert_response(42))),
    ):
        result = await observer.generate("gemma2-2b", "Hello")

    assert "_liteops" in result
    meta = result["_liteops"]
    assert meta["device_id"] == "test-hub-01"
    assert meta["model"] == "gemma2-2b"
    assert meta["mem_rss_mb"] == pytest.approx(400.0, abs=1.0)
    assert meta["output_tokens"] == 42
    assert meta["latency_ms"] >= 0


@pytest.mark.asyncio
async def test_generate_never_returns_zero_memory_when_process_found(observer: LiteRTObserver) -> None:
    proc = _make_proc(rss_bytes=1 * 1_048_576)  # 1 MB minimum — not 0

    with (
        patch("psutil.Process", return_value=proc),
        patch.object(observer, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        result = await observer.generate("gemma2-2b", "Test")

    assert result["_liteops"]["mem_rss_mb"] > 0.0, "mem_rss_mb must never be 0.0 when process exists"


@pytest.mark.asyncio
async def test_peak_memory_only_increases(observer: LiteRTObserver) -> None:
    low_proc  = _make_proc(rss_bytes=300 * 1_048_576)
    high_proc = _make_proc(rss_bytes=600 * 1_048_576)

    with (
        patch("psutil.Process", return_value=low_proc),
        patch.object(observer, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        r1 = await observer.generate("gemma2-2b", "First call")

    with (
        patch("psutil.Process", return_value=high_proc),
        patch.object(observer, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        r2 = await observer.generate("gemma2-2b", "Second call")

    # Peak after second call must be >= peak after first call
    assert r2["_liteops"]["mem_peak_mb"] >= r1["_liteops"]["mem_peak_mb"]

    # Now simulate memory dropping — peak must stay high
    with (
        patch("psutil.Process", return_value=low_proc),
        patch.object(observer, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        r3 = await observer.generate("gemma2-2b", "Third call — memory dropped")

    assert r3["_liteops"]["mem_peak_mb"] == pytest.approx(600.0, abs=1.0)


# ── Process discovery ─────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_auto_discovers_pid_by_process_name() -> None:
    obs = LiteRTObserver(
        litert_url="http://localhost:8000",
        device_id="test-hub-02",
        litert_pid=None,
        litert_process_name="lit",
    )
    fake_proc = _make_proc(rss_bytes=200 * 1_048_576)
    fake_proc.info = {"pid": 12345, "cmdline": ["python", "lit", "serve", "--model", "gemma"]}

    with (
        patch("psutil.process_iter", return_value=[fake_proc]),
        patch("psutil.Process", return_value=fake_proc),
        patch.object(obs, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        result = await obs.generate("gemma2-2b", "Discover PID test")

    assert obs._pid == 12345
    assert result["_liteops"]["mem_rss_mb"] == pytest.approx(200.0, abs=1.0)


# ── Fallback path ─────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_falls_back_to_zero_when_no_process_found() -> None:
    obs = LiteRTObserver(
        litert_url="http://localhost:8000",
        device_id="test-hub-03",
        litert_pid=None,
    )

    with (
        patch("psutil.process_iter", return_value=[]),
        patch.object(obs, "_call_litert", new=AsyncMock(return_value=_fake_litert_response())),
    ):
        result = await obs.generate("gemma2-2b", "No process test")

    assert result["_liteops"]["mem_rss_mb"] == 0.0


# ── Error path ────────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_http_error_propagates(observer: LiteRTObserver) -> None:
    import httpx

    proc = _make_proc()
    with (
        patch("psutil.Process", return_value=proc),
        patch.object(
            observer,
            "_call_litert",
            new=AsyncMock(side_effect=httpx.HTTPStatusError("500", request=MagicMock(), response=MagicMock())),
        ),
    ):
        with pytest.raises(httpx.HTTPStatusError):
            await observer.generate("gemma2-2b", "This should fail")


# ── Snapshot ──────────────────────────────────────────────────────────────────


def test_snapshot_returns_current_rss(observer: LiteRTObserver) -> None:
    proc = _make_proc(rss_bytes=350 * 1_048_576)

    with patch("psutil.Process", return_value=proc):
        snap = observer.snapshot("gemma2-2b")

    assert isinstance(snap, Snapshot)
    assert snap.rss_mb == pytest.approx(350.0, abs=1.0)
    assert snap.device_id == "test-hub-01"
    assert snap.model == "gemma2-2b"


def test_snapshot_as_dict(observer: LiteRTObserver) -> None:
    proc = _make_proc()
    with patch("psutil.Process", return_value=proc):
        snap = observer.snapshot("gemma2-2b")

    d = snap.as_dict()
    assert set(d.keys()) == {"device_id", "model", "rss_mb", "peak_mb"}


# ── _read_rss_mb helper ───────────────────────────────────────────────────────


def test_read_rss_mb_returns_zero_on_none() -> None:
    assert _read_rss_mb(None) == 0.0


def test_read_rss_mb_returns_zero_on_no_such_process() -> None:
    dead_proc = MagicMock(spec=psutil.Process)
    dead_proc.memory_info.side_effect = psutil.NoSuchProcess(pid=0)
    assert _read_rss_mb(dead_proc) == 0.0


def test_read_rss_mb_converts_bytes_to_mb() -> None:
    proc = _make_proc(rss_bytes=1_048_576)  # exactly 1 MB
    assert _read_rss_mb(proc) == pytest.approx(1.0)
