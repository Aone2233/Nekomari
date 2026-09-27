"""Insert the maintenance page's i18n keys, preserving the locale files' formatting."""
import json, pathlib, sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCALES = ROOT / "src" / "i18n" / "locales"

ENTRIES = {
    "en": {
        "title": "Maintenance windows",
        "subtitle": "While a window is open, its nodes' alerts are suppressed. Metrics keep recording.",
        "creating": "New window", "editing": "Editing",
        "new": "new",
        "namePlaceholder": "name",
        "reasonPlaceholder": "reason (optional)",
        "scope": "Scope",
        "scopeAll": "every node",
        "scopeAllBadge": "every node",
        "save": "Save window",
        "cancelEdit": "Cancel edit",
        "windowsHeading": "Windows",
        "openCount": "{{count}} open now",
        "loading": "Loading windows…",
        "empty": "No maintenance windows.",
        "name": "Name",
        "window": "Window",
        "state": "State",
        "openFor": "open, {{remaining}} left",
        "closed": "closed",
        "edit": "Edit",
        "delete": "Delete",
        "error": "The window was not saved",
    },
    "zh_CN": {
        "title": "维护窗口",
        "subtitle": "窗口开启期间，其中节点的告警会被抑制。指标照常采集。",
        "creating": "新建窗口", "editing": "正在编辑",
        "new": "新建",
        "namePlaceholder": "名称",
        "reasonPlaceholder": "原因（可选）",
        "scope": "范围",
        "scopeAll": "全部节点",
        "scopeAllBadge": "全部节点",
        "save": "保存窗口",
        "cancelEdit": "取消编辑",
        "windowsHeading": "窗口列表",
        "openCount": "{{count}} 个正在生效",
        "loading": "正在加载窗口…",
        "empty": "暂无维护窗口。",
        "name": "名称",
        "window": "时间窗",
        "state": "状态",
        "openFor": "生效中，剩余 {{remaining}}",
        "closed": "未生效",
        "edit": "编辑",
        "delete": "删除",
        "error": "窗口未保存",
    },
}

def insert(path, entries):
    text = path.read_text(encoding="utf-8")
    if "maintenance" in json.loads(text):
        print(f"  {path.name}: 'maintenance' already present, left alone"); return
    body = json.dumps({"maintenance": entries}, ensure_ascii=False, indent=2)
    inner = body[body.index("{") + 1 : body.rindex("}")].rstrip()
    marker = text.rindex("}")
    updated = text[:marker].rstrip()
    if not updated.endswith(","): updated += ","
    updated += "\n" + inner + "\n}\n"
    path.write_text(updated, encoding="utf-8")
    print(f"  {path.name}: added {len(json.loads(path.read_text(encoding='utf-8'))['maintenance'])} keys")

for locale, entries in ENTRIES.items():
    insert(LOCALES / f"{locale}.json", entries)