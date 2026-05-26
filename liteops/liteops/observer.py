"""
LiteRT observer — transparent observability wrapper for a running lit serve process.

Fixes the mem_mb: 0.0 gap present in every unmonitored LiteRT deployment by reading
real process memory via psutil and exporting Prometheus metrics on every inference call.

Typical usage:
    observer = LiteRTObserver(
        litert_url="http://localhost:8000",
        device_id="hub-barcelona-01",
    )
    result = await observer.generate("gemma2-2b-it-q4", "Explain edge AI in one sentence.")
    print(result["_liteops"]["mem_rss_mb"])  # e.g. 412.3  — never 0.0
"""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from typing import Any

import httpx
import psutil
from prometheus_client import Counter, Gauge, Histogram, start_http_server

logger = logging.getLogger(__name__)

# ── Prometheus metric definitions ─────────────────────────────────────────────
# All metrics use the "liteops_litert_" prefix for easy dashboard filtering.

_MEMORY_RSS_MB = Gauge(
    "liteops_litert_memory_rss_mb",
    "LiteRT process RSS memory in MB",
    ["device_id", "model"],
)
_MEMORY_PEAK_MB = Gauge(
    "liteops_litert_memory_peak_mb",
    "LiteRT process peak RSS memory observed in MB",
    ["device_id", "model"],
)
_REQUEST_LATENCY_MS = Histogram(
    "liteops_litert_request_latency_ms",
    "End-to-end generateContent latency in milliseconds",
    ["device_id", "model"],
    buckets=[50, 100, 200, 500, 1_000, 2_000, 5_000, 10_000],
)
_TOKENS_GENERATED = Counter(
    "liteops_litert_tokens_generated_total",
    "Cumulative output tokens generated",
    ["device_id", "model"],
)
_REQUESTS_TOTAL = Counter(
    "liteops_litert_requests_total",
    "Total generateContent requests by outcome",
    ["device_id", "model", "status"],  # status: "ok" | "error"
)


# ── Public data types ─────────────────────────────────────────────────────────


@dataclass(frozen=True)
class Snapshot:
    """Point-in-time memory snapshot for a running LiteRT process."""

    device_id: str
    model: str
    rss_mb: float
    peak_mb: float

    def as_dict(self) -> dict[str, Any]:
        return {
            "device_id": self.device_id,
            "model": self.model,
            "rss_mb": self.rss_mb,
            "peak_mb": self.peak_mb,
        }


@dataclass
class _Telemetry:
    """Internal telemetry collected around a single inference call."""

    mem_rss_mb: float
    mem_peak_mb: float
    latency_ms: float
    output_tokens: int
    status: str  # "ok" | "error"


# ── Observer ─────────────────────────────────────────────────────────────────


class LiteRTObserver:
    """
    Transparent observability wrapper for a running lit serve instance.

    Wraps every generateContent call to capture real memory usage, latency,
    and token throughput — fixing the mem_mb: 0.0 gap that exists in every
    LiteRT deployment that has no sidecar observability.

    The observer does NOT replace lit serve; it sits alongside it.
    Set metrics_port to expose a /metrics endpoint for Prometheus scraping.

    Args:
        litert_url:          Base URL of the running lit serve process.
        device_id:           Stable identifier for this edge device.
        litert_pid:          PID of the lit serve process. If None, the observer
                             searches for a process whose cmdline contains
                             litert_process_name at the first generate() call.
        litert_process_name: Substring used to auto-discover the lit serve PID.
        metrics_port:        If set, starts a Prometheus /metrics HTTP server on
                             this port. Set to None to export metrics only in-process.
    """

    def __init__(
        self,
        litert_url: str,
        device_id: str,
        litert_pid: int | None = None,
        litert_process_name: str = "lit",
        metrics_port: int | None = None,
    ) -> None:
        self.litert_url = litert_url.rstrip("/")
        self.device_id = device_id
        self._pid = litert_pid
        self._process_name = litert_process_name
        self._peak_mb: dict[str, float] = {}

        if metrics_port is not None:
            start_http_server(metrics_port)
            logger.info("liteops: Prometheus metrics on :%d/metrics", metrics_port)

    # ── Public API ────────────────────────────────────────────────────────────

    async def generate(
        self,
        model: str,
        prompt: str,
        max_output_tokens: int = 256,
        timeout_s: float = 60.0,
    ) -> dict[str, Any]:
        """
        Call lit serve's generateContent endpoint and record full telemetry.

        Returns the raw Gemini-compatible response dict with an added "_liteops"
        key containing the captured metrics for this call.

        Raises:
            httpx.HTTPStatusError: if lit serve returns a non-2xx status.
            httpx.TimeoutException:  if the call exceeds timeout_s.
        """
        proc = self._resolve_process()
        mem_before = _read_rss_mb(proc)
        t_start = time.perf_counter()

        try:
            resp = await self._call_litert(model, prompt, max_output_tokens, timeout_s)
        except Exception:
            _REQUESTS_TOTAL.labels(self.device_id, model, "error").inc()
            raise

        latency_ms = (time.perf_counter() - t_start) * 1_000
        mem_after = _read_rss_mb(proc)
        output_tokens = (
            resp.get("usageMetadata", {}).get("candidatesTokenCount", 0)
        )

        tel = self._record(model, mem_before, mem_after, latency_ms, output_tokens)

        resp["_liteops"] = {
            "device_id":     self.device_id,
            "model":         model,
            "mem_rss_mb":    round(tel.mem_rss_mb, 2),
            "mem_peak_mb":   round(tel.mem_peak_mb, 2),
            "latency_ms":    round(tel.latency_ms, 1),
            "output_tokens": tel.output_tokens,
        }
        return resp

    def snapshot(self, model: str = "") -> Snapshot:
        """Return a point-in-time memory snapshot without making an inference call."""
        proc = self._resolve_process()
        rss = _read_rss_mb(proc)
        peak = max(rss, self._peak_mb.get(model, 0.0))
        return Snapshot(
            device_id=self.device_id,
            model=model,
            rss_mb=round(rss, 2),
            peak_mb=round(peak, 2),
        )

    # ── Internal helpers ─────────────────────────────────────────────────────

    def _record(
        self,
        model: str,
        mem_before: float,
        mem_after: float,
        latency_ms: float,
        output_tokens: int,
    ) -> _Telemetry:
        peak = max(mem_before, mem_after, self._peak_mb.get(model, 0.0))
        self._peak_mb[model] = peak

        _MEMORY_RSS_MB.labels(self.device_id, model).set(mem_after)
        _MEMORY_PEAK_MB.labels(self.device_id, model).set(peak)
        _REQUEST_LATENCY_MS.labels(self.device_id, model).observe(latency_ms)
        if output_tokens:
            _TOKENS_GENERATED.labels(self.device_id, model).inc(output_tokens)
        _REQUESTS_TOTAL.labels(self.device_id, model, "ok").inc()

        return _Telemetry(
            mem_rss_mb=mem_after,
            mem_peak_mb=peak,
            latency_ms=latency_ms,
            output_tokens=output_tokens,
            status="ok",
        )

    async def _call_litert(
        self,
        model: str,
        prompt: str,
        max_output_tokens: int,
        timeout_s: float,
    ) -> dict[str, Any]:
        url = f"{self.litert_url}/v1beta/models/{model}:generateContent"
        payload = {
            "contents": [{"parts": [{"text": prompt}]}],
            "generationConfig": {"maxOutputTokens": max_output_tokens},
        }
        async with httpx.AsyncClient(timeout=timeout_s) as client:
            r = await client.post(url, json=payload)
            r.raise_for_status()
            return r.json()  # type: ignore[no-any-return]

    def _resolve_process(self) -> psutil.Process | None:
        """Return the lit serve psutil.Process, auto-discovering the PID if needed."""
        if self._pid is not None:
            try:
                return psutil.Process(self._pid)
            except psutil.NoSuchProcess:
                logger.warning("liteops: PID %d gone — re-discovering lit serve", self._pid)
                self._pid = None

        for proc in psutil.process_iter(["pid", "cmdline"]):
            try:
                cmdline = " ".join(proc.info["cmdline"] or [])
                if self._process_name in cmdline:
                    self._pid = proc.info["pid"]
                    logger.info("liteops: auto-discovered lit serve at PID %d", self._pid)
                    return proc
            except (psutil.NoSuchProcess, psutil.AccessDenied):
                continue

        logger.warning(
            "liteops: lit serve process not found (name=%r) — mem_mb will be 0.0",
            self._process_name,
        )
        return None


# ── Module-level helper ───────────────────────────────────────────────────────


def _read_rss_mb(proc: psutil.Process | None) -> float:
    """Read RSS memory in MB from a psutil Process. Returns 0.0 on any failure."""
    if proc is None:
        return 0.0
    try:
        return proc.memory_info().rss / 1_048_576
    except (psutil.NoSuchProcess, psutil.AccessDenied):
        return 0.0
