"""Insert the status page's i18n keys into the locale files, preserving formatting.

The locale files are hand-formatted, so a JSON round trip would reflow them; this does a
text insertion before the final closing brace instead, the same approach the ICMP keys
used. Only en and zh_CN are written: the other three locales are produced by
`npm run i18n:sync`, which needs an API key this environment does not have, and a
missing key falls back to English rather than breaking the page.
"""

import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCALES = ROOT / "src" / "i18n" / "locales"

ENTRIES = {
    "en": {
        "title": "Status and SLA",
        "subtitle": "Availability, latency and outages over the selected window, computed from stored metrics.",
        "window": "Window",
        "window_24h": "24 hours",
        "window_7d": "7 days",
        "window_30d": "30 days",
        "window_90d": "90 days",
        "loading": "Loading the report…",
        "error": "Could not load the report",
        "empty": "No nodes reported in this window.",
        "noData": "no data",
        "coverage": "Coverage",
        "coverageOf": "{{percent}} ({{observed}}/{{expected}} samples)",
        "availability": "Availability",
        "latency": "Latency p50/p95/p99 (ms)",
        "latencyValues": "{{p50}} / {{p95}} / {{p99}}",
        "outages": "Outages",
        "noOutages": "none",
        "reportingGaps": "Reporting gaps (not outages — the node stopped reporting)",
        "task": "Task",
        "untaggedSeries": "untagged series",
        "nodeNoData": "This node reported nothing in this window.",
        "sampleInterval": "Buckets of {{seconds}}s",
        "clamped": "Shortened to the retained range",
        "navStatus": "Status",
    },
    "zh_CN": {
        "title": "状态与 SLA",
        "subtitle": "按所选时间窗，从已存指标计算可用率、延迟与中断。",
        "window": "时间窗",
        "window_24h": "24 小时",
        "window_7d": "7 天",
        "window_30d": "30 天",
        "window_90d": "90 天",
        "loading": "正在加载报表…",
        "error": "无法加载报表",
        "empty": "该时间窗内没有任何节点上报。",
        "noData": "无数据",
        "coverage": "覆盖率",
        "coverageOf": "{{percent}}（{{observed}}/{{expected}} 个采样）",
        "availability": "可用率",
        "latency": "延迟 p50/p95/p99（毫秒）",
        "latencyValues": "{{p50}} / {{p95}} / {{p99}}",
        "outages": "中断",
        "noOutages": "无",
        "reportingGaps": "上报缺口（不是中断——只是节点停止上报）",
        "task": "任务",
        "untaggedSeries": "无标签序列",
        "nodeNoData": "该节点在此时间窗内没有上报。",
        "sampleInterval": "每桶 {{seconds}} 秒",
        "clamped": "已缩短到保留范围内",
        "navStatus": "状态",
    },
}


def insert(path: pathlib.Path, entries: dict) -> None:
    text = path.read_text(encoding="utf-8")
    existing = json.loads(text)
    if "status" in existing:
        print(f"  {path.name}: 'status' already present, left alone")
        return
    body = json.dumps({"status": entries}, ensure_ascii=False, indent=2)
    # Strip the outer braces so the block can be nested under the file's root object.
    inner = body[body.index("{") + 1 : body.rindex("}")].rstrip()
    marker = text.rindex("}")
    # Match the file's indentation for the new top-level key.
    updated = text[:marker].rstrip()
    if not updated.endswith(","):
        updated += ","
    updated += "\n" + inner + "\n}\n"
    path.write_text(updated, encoding="utf-8")
    added = len(json.loads(path.read_text(encoding="utf-8"))["status"])
    print(f"  {path.name}: added {added} keys under 'status'")


def main() -> int:
    for locale, entries in ENTRIES.items():
        path = LOCALES / f"{locale}.json"
        if not path.exists():
            print(f"  {path.name}: missing", file=sys.stderr)
            return 1
        insert(path, entries)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
