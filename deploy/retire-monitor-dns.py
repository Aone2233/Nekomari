#!/usr/bin/env python3
"""Delete the DNS record that fronts the retired CF-Server-Monitor worker.

monitor.orderly2233.org is an AAAA record pointing at 100:: (the Cloudflare
worker placeholder), which is how a Worker gets bound to a hostname. Deleting the
record makes the hostname unresolvable, so the panel and its /update endpoint stop
being reachable.

The Worker itself lives in the Cloudflare dashboard and cannot be removed with
this token -- it is DNS-scoped (proven earlier: purge_cache returned
"Authentication error"). Deleting the route is therefore what actually retires
the endpoint from the outside; the dashboard entry is left for the owner to
delete, and this script says so rather than pretending it is gone.

Usage: retire-monitor-dns.py [--dry-run]
"""
import base64
import json
import re
import sys
import urllib.error
import urllib.request

CERT = "/root/.cloudflared/cert.pem"
HOST = "monitor.orderly2233.org"
API = "https://api.cloudflare.com/client/v4"

dry = "--dry-run" in sys.argv

raw = open(CERT).read()
m = re.search(
    r"-----BEGIN ARGO TUNNEL TOKEN-----\s*(.*?)\s*-----END ARGO TUNNEL TOKEN-----",
    raw, re.S)
payload = json.loads(base64.b64decode(m.group(1)))
token, zone = payload["apiToken"], payload["zoneID"]


def cf(path, method="GET"):
    req = urllib.request.Request(API + path, method=method)
    req.add_header("Authorization", "Bearer " + token)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        return json.loads(e.read().decode() or "{}")


records = cf(f"/zones/{zone}/dns_records?name={HOST}").get("result") or []
if not records:
    print(f"no record for {HOST} (already removed)")
    raise SystemExit(0)

for rec in records:
    print(f"found {rec['type']} {rec['name']} -> {rec['content']} "
          f"proxied={rec.get('proxied')} id={rec['id']}")
    if dry:
        print("  [dry-run] would delete")
        continue
    res = cf(f"/zones/{zone}/dns_records/{rec['id']}", "DELETE")
    print(f"  delete success={res.get('success')} errors={res.get('errors')}")

if not dry:
    left = cf(f"/zones/{zone}/dns_records?name={HOST}").get("result") or []
    print(f"\nremaining records for {HOST}: {len(left)}")
    print("NOTE: the Worker itself still exists in the Cloudflare dashboard "
          "(Workers & Pages) and its route binding must be removed there -- "
          "this DNS-scoped token cannot touch it.")
