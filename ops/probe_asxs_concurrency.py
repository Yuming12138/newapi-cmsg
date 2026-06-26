#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor


BODY = json.dumps(
    {
        "model": "gpt-5.5",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1,
        "stream": False,
    }
).encode("utf-8")


def get_key(channel_id: int) -> str:
    cmd = [
        "docker",
        "exec",
        "new-api-postgres",
        "psql",
        "-U",
        "newapi",
        "-d",
        "new-api",
        "-Atc",
        f"select key from channels where id={channel_id};",
    ]
    return subprocess.check_output(cmd, text=True).strip()


def do_call(url: str, api_key: str, timeout: int) -> tuple[int, float, str, str]:
    req = urllib.request.Request(
        url,
        data=BODY,
        method="POST",
        headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "asxs-concurrency-probe/1.0",
        },
    )
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = resp.status
            raw = resp.read(200)
            err = ""
    except urllib.error.HTTPError as exc:
        status = exc.code
        raw = exc.read(200)
        err = f"HTTPError {exc.code}"
    except Exception as exc:
        status = 0
        raw = b""
        err = type(exc).__name__ + ": " + str(exc)
    elapsed = time.perf_counter() - start
    body = raw.decode("utf-8", "replace").replace("\n", " ").replace("\r", " ")
    return status, elapsed, err, body


def run_batch(label: str, api_key: str, n: int, url: str, timeout: int) -> bool:
    gate = threading.Event()
    results = []

    def worker():
        gate.wait()
        return do_call(url, api_key, timeout)

    with ThreadPoolExecutor(max_workers=n) as ex:
        futs = [ex.submit(worker) for _ in range(n)]
        time.sleep(0.2)
        t0 = time.perf_counter()
        gate.set()
        for fut in futs:
            results.append(fut.result())
        batch_elapsed = time.perf_counter() - t0

    ok = sum(1 for status, *_ in results if status == 200)
    bad = len(results) - ok
    max_elapsed = max(elapsed for _, elapsed, _, _ in results)
    statuses = sorted({status for status, *_ in results})
    print(
        f"{label} n={n} ok={ok}/{len(results)} bad={bad} "
        f"statuses={statuses} batch_elapsed={batch_elapsed:.2f}s "
        f"max_req_elapsed={max_elapsed:.2f}s"
    )
    for i, (status, elapsed, err, body) in enumerate(results, 1):
        if status != 200:
            print(
                f"  fail#{i}: status={status} elapsed={elapsed:.2f}s "
                f"err={err} body={body[:120]}"
            )
    return bad == 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--channels", default="1,2")
    parser.add_argument("--levels", default="1,8,12")
    parser.add_argument("--url", default="https://api.asxs.top/v1/chat/completions")
    parser.add_argument("--timeout", type=int, default=60)
    args = parser.parse_args()

    levels = [int(x) for x in args.levels.split(",") if x.strip()]
    labels = {"1": "cgm", "2": "mg"}
    for channel in [x.strip() for x in args.channels.split(",") if x.strip()]:
        label = labels.get(channel, f"channel-{channel}")
        key = get_key(int(channel))
        print(f"== {label} ==")
        for level in levels:
            run_batch(label, key, level, args.url, args.timeout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
