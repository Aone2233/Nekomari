"""Insert the bulk page's i18n keys, preserving the locale files' formatting.

Same approach as add-status-locale-keys.py: a text insertion before the final closing brace
rather than a JSON round trip, because the locale files are hand-formatted and re-serialising
them reflows every line. Only en and zh_CN are written; the other three locales are produced
by `npm run i18n:sync`, which needs an API key this environment does not have, and a missing
key falls back to English rather than breaking the page.
"""

import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCALES = ROOT / "src" / "i18n" / "locales"

ENTRIES = {
    "en": {
        "title": "Bulk edit",
        "subtitle": "Apply one change to many nodes. Only the fields you switch on are sent.",
        "fieldsHeading": "Fields to change",
        "fieldsHint": "A field is only sent when its switch is on, so the other fields on every selected node keep their values.",
        "nodesHeading": "Nodes",
        "selectAll": "Select all",
        "selectNone": "Select none",
        "selected": "Selected",
        "node": "Node",
        "group": "Group",
        "weight": "Weight",
        "apply": "Apply to selection",
        "selectionCount": "{{count}} selected",
        "fieldCount": "{{count}} fields to send",
        "noFieldsHint": "Switch on at least one field.",
        "loading": "Loading nodes…",
        "error": "The edit could not be sent",
        "reportSummary": "{{applied}} applied, {{failed}} failed of {{total}}",
        "failuresHeading": "Not applied",
        "unknownError": "no reason given",
        "fieldsApplied": "Fields sent: {{fields}}",
        "field": {
            "group": "Group",
            "tags": "Tags",
            "weight": "Weight",
            "hidden": "Hidden",
            "price": "Price",
            "billingCycle": "Billing cycle",
            "currency": "Currency",
            "trafficLimit": "Traffic limit",
            "trafficLimitType": "Limit type",
        },
        "hidden": {"yes": "hidden", "no": "visible"},
    },
    "zh_CN": {
        "title": "批量编辑",
        "subtitle": "把一项改动应用到多个节点。只有你打开开关的字段会被发送。",
        "fieldsHeading": "要改的字段",
        "fieldsHint": "只有开关打开的字段才会被发送，其余字段在所有选中节点上保持原值。",
        "nodesHeading": "节点",
        "selectAll": "全选",
        "selectNone": "全不选",
        "selected": "选中",
        "node": "节点",
        "group": "分组",
        "weight": "权重",
        "apply": "应用到选中项",
        "selectionCount": "已选 {{count}} 个",
        "fieldCount": "将发送 {{count}} 个字段",
        "noFieldsHint": "至少打开一个字段。",
        "loading": "正在加载节点…",
        "error": "无法发送本次编辑",
        "reportSummary": "{{total}} 个中：成功 {{applied}}，失败 {{failed}}",
        "failuresHeading": "未应用",
        "unknownError": "未给出原因",
        "fieldsApplied": "已发送字段：{{fields}}",
        "field": {
            "group": "分组",
            "tags": "标签",
            "weight": "权重",
            "hidden": "隐藏",
            "price": "价格",
            "billingCycle": "计费周期",
            "currency": "货币",
            "trafficLimit": "流量阈值",
            "trafficLimitType": "阈值类型",
        },
        "hidden": {"yes": "隐藏", "no": "可见"},
    },
}


def insert(path: pathlib.Path, entries: dict) -> None:
    text = path.read_text(encoding="utf-8")
    existing = json.loads(text)
    if "bulk" in existing:
        print(f"  {path.name}: 'bulk' already present, left alone")
        return
    body = json.dumps({"bulk": entries}, ensure_ascii=False, indent=2)
    inner = body[body.index("{") + 1 : body.rindex("}")].rstrip()
    marker = text.rindex("}")
    updated = text[:marker].rstrip()
    if not updated.endswith(","):
        updated += ","
    updated += "\n" + inner + "\n}\n"
    path.write_text(updated, encoding="utf-8")
    added = len(json.loads(path.read_text(encoding="utf-8"))["bulk"])
    print(f"  {path.name}: added {added} keys under 'bulk'")


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
