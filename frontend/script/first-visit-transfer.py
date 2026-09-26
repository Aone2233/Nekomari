"""B1: measure a first visit's real transfer against a running panel.

    python script/_b1_panel.py https://komari.orderly2233.org [runs]

Answers the roadmap's B1 question — what a first visit actually downloads, and
what the service worker caches — with three numbers per run: first-visit transfer
split by resource kind, the repeat visit, and whether an offline navigation still
mounts the app.

Two things make it the honest version of the measurement:

  * one fresh browser **context** per run, so each is a cold visit (a new context
    does not share the HTTP cache or the service-worker registration)
  * it asks whether the app **mounted** offline, not whether the body had text: the
    shell is a placeholder until React runs, so a non-empty body proves nothing

Point it at production rather than at a local instance if the question is what
users pay: a panel behind nginx + Cloudflare is compressed (br) while the Go server
alone is not, and that difference is 3.5x. See `docs/ROADMAP.md` B1.
"""

import sys
from collections import defaultdict
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:25774").rstrip("/")
RUNS = int(sys.argv[2]) if len(sys.argv) > 2 else 3
HOST = urlparse(BASE).netloc


def kind_of(url: str) -> str:
    path = urlparse(url).path
    if path.endswith(".js"):
        return "js"
    if path.endswith(".css"):
        return "css"
    if path.endswith((".png", ".webp", ".jpg", ".svg", ".ico")):
        return "image"
    if path.endswith((".json", ".webmanifest")):
        return "json"
    if path in ("/", "/index.html") or path.endswith(".html"):
        return "html"
    if path.startswith("/api/"):
        return "api"
    return "other"


def collect(session, found):
    """Record encoded size per finished request."""

    def on_loading_finished(params):
        url = params.get("response", {}).get("url", "")
        size = params.get("encodedDataLength", 0)
        if url:
            found.append((url, size))

    session.on("Network.loadingFinished", on_loading_finished)


def main() -> int:
    summaries = []
    with sync_playwright() as p:
        # One browser for all runs; a fresh *context* per run. The HTTP cache and
        # the service worker registration are per-context, so each run is a cold
        # visit, and using one process keeps the runs comparable to each other.
        browser = p.chromium.launch(headless=True)
        for run in range(1, RUNS + 1):
            context = browser.new_context()
            page = context.new_page()
            page.set_default_timeout(20000)

            found = []

            def on_response(response):
                try:
                    sizes = response.request.sizes()
                except Exception:
                    return
                size = sizes.get("responseHeadersSize", 0) + sizes.get("responseBodySize", 0)
                encoding = ""
                from_sw = False
                try:
                    encoding = response.all_headers().get("content-encoding", "")
                    from_sw = response.header_value("x-from-service-worker") is not None
                except Exception:
                    pass
                found.append((response.url, size, encoding, from_sw))

            page.on("response", on_response)
            page.goto(BASE, wait_until="load")
            # Precaching continues past `load`; a first visit is not finished there.
            page.wait_for_timeout(7000)
            page.remove_listener("response", on_response)

            transfer = defaultdict(int)
            counts = defaultdict(int)
            biggest = []
            for url, size, encoding, _from_sw in found:
                kind = kind_of(url)
                transfer[kind] += size
                counts[kind] += 1
                biggest.append((urlparse(url).path, size, encoding))

            total = sum(transfer.values())

            caches = page.evaluate("""async () => {
                const out = {};
                for (const name of await caches.keys()) {
                    const cache = await caches.open(name);
                    out[name] = (await cache.keys()).length;
                }
                return out;
            }""")
            shell = page.evaluate("""async () => {
                const found = [];
                for (const name of await caches.keys()) {
                    const cache = await caches.open(name);
                    for (const req of await cache.keys()) {
                        const path = new URL(req.url).pathname;
                        if (path === '/' || path.endsWith('.html')) found.push(path);
                    }
                }
                return found;
            }""")

            entry = {
                "run": run, "total": total, "by_kind": dict(transfer),
                "counts": dict(counts), "caches": caches, "shell": shell,
                "biggest": sorted(biggest, key=lambda r: -r[1])[:6],
            }

            # Warm visit in the same context.
            warm = []
            page.on("response", lambda r: warm.append(
                r.request.sizes().get("responseHeadersSize", 0)
                + r.request.sizes().get("responseBodySize", 0)))
            page.goto(BASE, wait_until="load")
            page.wait_for_timeout(2500)
            entry["warm"] = sum(warm)

            # Offline navigation, once. "Did it render" is asked of the app, not of
            # the body text: the shell is a placeholder until React mounts, so a
            # non-zero body length proves nothing.
            if run == 1:
                entry["online_marker"] = page.evaluate(
                    "() => !!document.querySelector('.radix-themes, #root > *')")
                context.set_offline(True)
                failures = []
                page.on("requestfailed", lambda r: failures.append(f"{r.url} :: {r.failure}"))
                try:
                    page.goto(BASE, wait_until="domcontentloaded", timeout=12000)
                    page.wait_for_timeout(2500)
                    entry["offline"] = {
                        "loaded": True,
                        "body_chars": page.evaluate("() => document.body.innerText.trim().length"),
                        "app_mounted": page.evaluate(
                            "() => !!document.querySelector('.radix-themes, #root > *')"),
                        "html_chars": page.evaluate("() => document.documentElement.outerHTML.length"),
                    }
                except Exception as error:
                    entry["offline"] = {"loaded": False, "error": str(error)[:110]}
                entry["offline"]["failed"] = failures[:4]
                context.set_offline(False)

            summaries.append(entry)
            context.close()
        browser.close()

    for entry in summaries:
        print(f"--- run {entry['run']} ---")
        print(f"  first visit: {entry['total'] / 1024:.0f} KiB "
              f"({entry['total'] / 1024 / 1024:.2f} MiB)")
        for kind, size in sorted(entry["by_kind"].items(), key=lambda kv: -kv[1]):
            print(f"      {kind:<6} {size / 1024:8.0f} KiB {entry['counts'][kind]:4d} requests")
        print("  biggest:")
        for path, size, encoding in entry["biggest"]:
            print(f"      {size / 1024:8.0f} KiB enc={encoding or '-':<6} {path[:52]}")
        print(f"  warm visit: {entry['warm'] / 1024:.0f} KiB")
        print(f"  caches: {entry['caches']}  shell entries: {entry['shell'] or 'NONE'}")
        if "offline" in entry:
            print(f"  offline: {entry['offline']}")
        print()

    totals = [entry["total"] for entry in summaries]
    print(f"first visit over {len(totals)} runs: "
          f"min {min(totals) / 1024:.0f} / median {sorted(totals)[len(totals) // 2] / 1024:.0f} "
          f"/ max {max(totals) / 1024:.0f} KiB")
    return 0


if __name__ == "__main__":
    sys.exit(main())
