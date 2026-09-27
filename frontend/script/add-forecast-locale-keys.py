"""Insert the traffic forecast page's i18n keys, preserving the locale files' formatting."""
import json, pathlib

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCALES = ROOT / "src" / "i18n" / "locales"
ENTRIES = {
    "en": {
        "title": "Traffic forecast",
        "subtitle": "Where each node's traffic is heading this cycle, and when it would reach its limit. A projection is shown with the data behind it.",
        "cycleWindow": "This cycle",
        "cycleDay": "Cycle day",
        "timezone": "Computed in",
        "threshold": "warns at {{percent}}%",
        "preview": "Preview",
        "saveDay": "Save day",
        "error": "Could not apply",
        "exceeding": "{{count}} projected over the limit",
        "projected": "{{count}} projected",
        "refused": "{{count}} with too little data",
        "loading": "Loading projections…",
        "node": "Node",
        "used": "Used",
        "projectedEnd": "Projected end",
        "limit": "Limit",
        "crosses": "Limit reached in",
        "basis": "Basis",
        "noLimit": "no limit",
        "insufficient": "not enough data",
        "inDays": "{{days}} days",
        "notThisCycle": "not this cycle",
        "basisDetail": "{{samples}} samples, {{coverage}}% of cycle, ±{{uncertainty}}%",
    },
    "zh_CN": {
        "title": "流量预测",
        "subtitle": "本周期各节点的流量走向，以及何时会触顶。每个预测都会附上它依据的数据。",
        "cycleWindow": "本周期",
        "cycleDay": "账期日",
        "timezone": "计算时区",
        "threshold": "{{percent}}% 时告警",
        "preview": "预览",
        "saveDay": "保存账期日",
        "error": "无法应用",
        "exceeding": "{{count}} 个预计超限",
        "projected": "{{count}} 个已预测",
        "refused": "{{count}} 个数据不足",
        "loading": "正在计算预测…",
        "node": "节点",
        "used": "已用",
        "projectedEnd": "预计周期末",
        "limit": "限量",
        "crosses": "触顶剩余",
        "basis": "依据",
        "noLimit": "未设限量",
        "insufficient": "数据不足",
        "inDays": "{{days}} 天",
        "notThisCycle": "本周期不会",
        "basisDetail": "{{samples}} 个样本，覆盖周期 {{coverage}}%，±{{uncertainty}}%",
    },
}
def insert(path, entries):
    text = path.read_text(encoding="utf-8")
    if "forecast" in json.loads(text):
        print(f"  {path.name}: 'forecast' already present"); return
    body = json.dumps({"forecast": entries}, ensure_ascii=False, indent=2)
    inner = body[body.index("{") + 1 : body.rindex("}")].rstrip()
    marker = text.rindex("}")
    updated = text[:marker].rstrip()
    if not updated.endswith(","): updated += ","
    updated += "\n" + inner + "\n}\n"
    path.write_text(updated, encoding="utf-8")
    print(f"  {path.name}: added {len(json.loads(path.read_text(encoding='utf-8'))['forecast'])} keys")
for locale, entries in ENTRIES.items():
    insert(LOCALES / f"{locale}.json", entries)