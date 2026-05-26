"""
Quickstart observer entrypoint.
Starts a Prometheus metrics server and polls lit serve memory passively.
For active per-request telemetry, wrap your own generate() calls with LiteRTObserver.
"""

import asyncio
import logging
import os
import time

from liteops.observer import LiteRTObserver

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

LITERT_URL   = os.environ["LITERT_URL"]
DEVICE_ID    = os.environ.get("DEVICE_ID", "quickstart-device-01")
METRICS_PORT = int(os.environ.get("METRICS_PORT", "9101"))
POLL_INTERVAL_S = 5


async def main() -> None:
    observer = LiteRTObserver(
        litert_url=LITERT_URL,
        device_id=DEVICE_ID,
        metrics_port=METRICS_PORT,
    )
    logging.info("Observer started — polling %s every %ds", LITERT_URL, POLL_INTERVAL_S)

    while True:
        snap = observer.snapshot()
        logging.info(
            "device=%s rss_mb=%.1f peak_mb=%.1f",
            snap.device_id, snap.rss_mb, snap.peak_mb,
        )
        await asyncio.sleep(POLL_INTERVAL_S)


if __name__ == "__main__":
    asyncio.run(main())
