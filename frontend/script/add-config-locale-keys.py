"""Insert the config page's i18n keys, preserving the locale files' formatting."""
import json, pathlib

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCALES = ROOT / "src" / "i18n" / "locales"
ENTRIES = {
    "en": {
        "title": "Configuration export and import",
        "subtitle": "Take the panel's nodes, tasks, maintenance windows, settings and notification policies as one file, and apply one back.",
        "export": "Export",
        "includeSecrets": "Include credentials (agent tokens, notification credentials)",
        "secretsWarning": "The file will carry credentials",
        "documentHeading": "Document",
        "documentPlaceholder": "Export to fill this in, or paste a document from another panel.",
        "dryRun": "Check what this would do",
        "import": "Import",
        "parseError": "Not a document",
        "runDryRunFirst": "Check the document first: the import button unlocks after a check.",
        "error": "The operation failed",
        "planSummary": "This would:",
        "resultSummary": "Imported:",
        "changeKind": "Action", "changeEntity": "Entity", "changeRecord": "Record", "changeFields": "Fields",
        "kind_create": "create", "kind_update": "update", "kind_unchanged": "unchanged", "kind_remove": "not in the document",
        "removalsNotDeleted": "{{count}} record(s) are in the panel and not in the document; they were left alone.",
    },
    "zh_CN": {
        "title": "配置导出与导入",
        "subtitle": "把面板的节点、任务、维护窗口、设置与通知策略导出为一个文件，也可以把一个文件应用回来。",
        "export": "导出",
        "includeSecrets": "包含凭据（agent token、通知凭据）",
        "secretsWarning": "该文件将携带凭据",
        "documentHeading": "文档",
        "documentPlaceholder": "点导出以填充这里，或粘贴来自另一个面板的文档。",
        "dryRun": "检查会发生什么",
        "import": "导入",
        "parseError": "不是有效文档",
        "runDryRunFirst": "请先检查文档：检查之后导入按钮才会解锁。",
        "error": "操作失败",
        "planSummary": "将会：",
        "resultSummary": "已导入：",
        "changeKind": "动作", "changeEntity": "对象", "changeRecord": "记录", "changeFields": "字段",
        "kind_create": "新建", "kind_update": "更新", "kind_unchanged": "无变化", "kind_remove": "不在文档中",
        "removalsNotDeleted": "有 {{count}} 条记录在面板里但不在文档中；它们被保留了。",
    },
}
def insert(path, entries):
    text = path.read_text(encoding="utf-8")
    if "config" in json.loads(text):
        print(f"  {path.name}: 'config' already present"); return
    body = json.dumps({"config": entries}, ensure_ascii=False, indent=2)
    inner = body[body.index("{") + 1 : body.rindex("}")].rstrip()
    marker = text.rindex("}")
    updated = text[:marker].rstrip()
    if not updated.endswith(","): updated += ","
    updated += "\n" + inner + "\n}\n"
    path.write_text(updated, encoding="utf-8")
    print(f"  {path.name}: added {len(json.loads(path.read_text(encoding='utf-8'))['config'])} keys")
for locale, entries in ENTRIES.items():
    insert(LOCALES / f"{locale}.json", entries)