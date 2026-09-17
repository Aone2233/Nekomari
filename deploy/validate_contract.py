#!/usr/bin/env python3
"""Validate the live ip-info responses against the theme's strict schemas.

The theme (LuminaPlus) parses both endpoints with zod object schemas, which strip
unknown keys and fail on a missing required key or a wrong type. This mirrors the
subset of zod behaviour that matters: required keys present, correct JSON type,
and `number | null` accepted where the schema is nullable (J = nullable string,
Y = nullable number, X = nullable boolean, m = boolean, d/u = string/int).

Usage: validate_contract.py <base-url> [cookie]
"""
import json
import sys
import urllib.request

BASE = sys.argv[1].rstrip("/")
COOKIE = sys.argv[2] if len(sys.argv) > 2 else None

failures = []


def get(path):
    req = urllib.request.Request(BASE + path)
    # Cloudflare's bot rule answers the default Python UA with 403; a browser UA is
    # what a real visitor sends, so use one when validating through the edge.
    req.add_header("User-Agent",
                   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                   "(KHTML, like Gecko) Chrome/140.0 Safari/537.36")
    if COOKIE:
        req.add_header("Cookie", COOKIE)
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def check(cond, msg):
    if not cond:
        failures.append(msg)


def want_type(value, types, path, nullable=False):
    if value is None:
        check(nullable, f"{path}: null not allowed")
        return
    check(isinstance(value, types),
          f"{path}: type {type(value).__name__} not in {types}")


def req_keys(obj, keys, path):
    for k in keys:
        check(k in obj, f"{path}: missing required key {k!r}")


# ---- status ----
s = get("/api/public/ip-info/v1/status")
check(s.get("ok") is True, "status: ok must be true")
d = s.get("data", {})
req_keys(d, ["available", "version", "schema_version", "mainland_china_excluded", "capabilities"], "status.data")
want_type(d.get("available"), bool, "status.data.available")
want_type(d.get("version"), str, "status.data.version")
want_type(d.get("schema_version"), int, "status.data.schema_version")
want_type(d.get("mainland_china_excluded"), bool, "status.data.mainland_china_excluded")
caps = d.get("capabilities", {})
req_keys(caps, ["geo", "network", "reputation", "media_unlock", "ai_unlock"], "status.data.capabilities")
for k in ("geo", "network", "reputation", "media_unlock", "ai_unlock"):
    want_type(caps.get(k), bool, f"status.data.capabilities.{k}")
print("status: checked")

# ---- lookup ----
lk = get("/api/public/ip-info/v1/lookup?uuid=probe&ip=8.8.8.8")
check(lk.get("ok") is True, "lookup: ok must be true")
d = lk.get("data", {})
req_keys(d, ["uuid", "schema_version", "excluded", "excluded_reason", "address",
             "location", "network", "reputation", "capabilities", "provider"], "lookup.data")
want_type(d.get("uuid"), str, "lookup.data.uuid")
want_type(d.get("schema_version"), int, "lookup.data.schema_version")
want_type(d.get("excluded"), bool, "lookup.data.excluded")
want_type(d.get("excluded_reason"), str, "lookup.data.excluded_reason", nullable=True)

addr = d.get("address", {})
req_keys(addr, ["value", "family"], "lookup.data.address")
want_type(addr.get("value"), str, "lookup.data.address.value")
check(addr.get("family") in (4, 6), f"lookup.data.address.family must be 4 or 6, got {addr.get('family')!r}")

loc = d.get("location", {})
req_keys(loc, ["continent", "country", "country_code", "region", "city", "timezone",
               "latitude", "longitude"], "lookup.data.location")
for k in ("continent", "country", "country_code", "region", "city", "timezone"):
    want_type(loc.get(k), str, f"lookup.data.location.{k}", nullable=True)
for k in ("latitude", "longitude"):
    want_type(loc.get(k), (int, float), f"lookup.data.location.{k}", nullable=True)

net = d.get("network", {})
req_keys(net, ["asn", "asn_number", "organization", "operator", "network_type",
               "route", "rir", "domain", "datacenter"], "lookup.data.network")
for k in ("asn", "organization", "operator", "network_type", "route", "rir", "domain", "datacenter"):
    want_type(net.get(k), str, f"lookup.data.network.{k}", nullable=True)
want_type(net.get("asn_number"), int, "lookup.data.network.asn_number", nullable=True)

rep = d.get("reputation", {})
req_keys(rep, ["available", "purity_score", "risk_score", "pollution_score", "risk_level",
               "pollution_level", "signals", "method"], "lookup.data.reputation")
want_type(rep.get("available"), bool, "lookup.data.reputation.available")
for k in ("purity_score", "risk_score", "pollution_score"):
    want_type(rep.get(k), (int, float), f"lookup.data.reputation.{k}")
for k in ("risk_level", "pollution_level"):
    want_type(rep.get(k), str, f"lookup.data.reputation.{k}", nullable=True)
sig = rep.get("signals", {})
req_keys(sig, ["proxy", "tor", "vpn", "datacenter", "abuser", "crawler"], "lookup.data.reputation.signals")
for k in ("proxy", "tor", "vpn", "datacenter", "abuser", "crawler"):
    want_type(sig.get(k), bool, f"lookup.data.reputation.signals.{k}", nullable=True)
m = rep.get("method", {})
req_keys(m, ["id", "status"], "lookup.data.reputation.method")

cl = d.get("classification")
if cl is not None:
    req_keys(cl, ["type", "label"], "lookup.data.classification")
    want_type(cl.get("type"), str, "lookup.data.classification.type")
    want_type(cl.get("label"), str, "lookup.data.classification.label")

prov = d.get("provider", {})
req_keys(prov, ["id", "name", "homepage", "base_source"], "lookup.data.provider")

meta = lk.get("meta", {})
req_keys(meta, ["cache", "stale", "updated_at", "expires_at", "stale_until", "warning"], "lookup.meta")
want_type(meta.get("cache"), str, "lookup.meta.cache")
want_type(meta.get("stale"), bool, "lookup.meta.stale")
for k in ("updated_at", "expires_at", "stale_until"):
    want_type(meta.get(k), str, f"lookup.meta.{k}")
want_type(meta.get("warning"), str, "lookup.meta.warning", nullable=True)
check(meta.get("cache") in ("hit", "miss"), f"lookup.meta.cache must be hit|miss, got {meta.get('cache')!r}")
print("lookup: checked")

# ---- latency ----
lt = get("/api/public/ip-info/v1/latency?uuid=probe&ip=8.8.8.8")
check(lt.get("ok") is True, "latency: ok must be true")
d = lt.get("data", {})
req_keys(d, ["uuid", "schema_version", "address", "classification", "latency", "provider"], "latency.data")
lat = d.get("latency", {})
req_keys(lat, ["nodes", "available_count", "timeout_count", "provider_cached"], "latency.data.latency")
want_type(lat.get("nodes"), list, "latency.data.latency.nodes")
for k in ("available_count", "timeout_count"):
    want_type(lat.get(k), int, f"latency.data.latency.{k}")
want_type(lat.get("provider_cached"), bool, "latency.data.latency.provider_cached")
for i, n in enumerate(lat.get("nodes") or []):
    p = f"latency.data.latency.nodes[{i}]"
    req_keys(n, ["id", "name", "city", "country_code", "latency_ms", "status"], p)
    for k in ("id", "name", "city", "country_code"):
        want_type(n.get(k), str, f"{p}.{k}")
    want_type(n.get("latency_ms"), (int, float), f"{p}.latency_ms", nullable=True)
    check(n.get("status") in ("ok", "timeout", "unavailable"),
          f"{p}.status must be ok|timeout|unavailable, got {n.get('status')!r}")
    cc = n.get("country_code")
    check(cc == "" or (len(cc) == 2 and cc.isupper()),
          f"{p}.country_code must be an uppercase 2-letter code, got {cc!r}")
meta = lt.get("meta", {})
req_keys(meta, ["cache", "stale", "updated_at", "expires_at", "stale_until", "warning"], "latency.meta")
print(f"latency: checked ({len(lat.get('nodes') or [])} nodes)")

# ---- error shape ----
try:
    get("/api/public/ip-info/v1/lookup?uuid=x&ip=10.0.0.1")
    failures.append("private IP should have been rejected")
except urllib.error.HTTPError as e:
    body = json.loads(e.read().decode())
    check(400 <= e.code < 500, f"private IP status should be 4xx, got {e.code}")
    check("error" in body and "message" in body.get("error", {}),
          f"error body must be {{error:{{message}}}}, got {body}")
    print(f"private IP rejected: HTTP {e.code} {body.get('error', {}).get('message', '')[:60]}")

print()
if failures:
    print(f"FAILURES ({len(failures)}):")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("ALL CONTRACT CHECKS PASSED")
