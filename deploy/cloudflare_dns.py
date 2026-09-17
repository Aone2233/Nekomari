#!/usr/bin/env python3
"""Set a Cloudflare DNS record so a hostname reaches this host's nginx.

Why a script: the zone routes hostnames to origins two different ways, and
picking the wrong one produces a hostname that resolves but serves nothing.

  * A proxied **A record to the origin** is what every working hostname in this
    zone uses (git/vault/blog/dav/subnc). UFW allows 80/443 from Cloudflare's
    ranges only, so this is the intended path.
  * A **CNAME into a cloudflared tunnel** only works for hostnames the tunnel
    actually carries. The tunnel here is *token-managed* -- its ingress lives in
    the Cloudflare dashboard -- so a hostname that is not in that ingress gets a
    record that resolves and then serves nothing.

This script therefore defaults to the A-record form and refuses to guess.

Credential: reuses the API token that `cloudflared tunnel login` wrote into
cert.pem, so no separate secret is needed. The token is DNS-scoped; it cannot
read or change SSL settings, which is why Total TLS / advanced certificates must
be enabled by hand in the dashboard.

Usage:
  cloudflare_dns.py list
  cloudflare_dns.py show <hostname>
  cloudflare_dns.py point <hostname> <origin-ip> [--dry-run]
"""
import argparse
import base64
import json
import re
import sys
import urllib.error
import urllib.request

DEFAULT_CERT = "/root/.cloudflared/cert.pem"
API = "https://api.cloudflare.com/client/v4"


def load_token(cert_path):
    raw = open(cert_path).read()
    match = re.search(
        r"-----BEGIN ARGO TUNNEL TOKEN-----\s*(.*?)\s*-----END ARGO TUNNEL TOKEN-----",
        raw, re.S)
    if not match:
        raise SystemExit(f"no ARGO TUNNEL TOKEN block in {cert_path}")
    payload = json.loads(base64.b64decode(match.group(1)))
    return payload["apiToken"], payload["zoneID"]


class Cloudflare:
    def __init__(self, token):
        self.token = token

    def call(self, path, method="GET", body=None):
        req = urllib.request.Request(
            API + path, method=method,
            data=json.dumps(body).encode() if body is not None else None)
        req.add_header("Authorization", "Bearer " + self.token)
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=25) as resp:
                return json.load(resp)
        except urllib.error.HTTPError as exc:
            return json.loads(exc.read().decode() or "{}")


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--cert", default=DEFAULT_CERT)
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("list", help="list every record in the zone")

    show = sub.add_parser("show", help="show the records for one hostname")
    show.add_argument("hostname")

    point = sub.add_parser("point", help="point a hostname at the origin (proxied A)")
    point.add_argument("hostname")
    point.add_argument("origin_ip")
    point.add_argument("--dry-run", action="store_true")

    args = parser.parse_args()
    token, zone = load_token(args.cert)
    cf = Cloudflare(token)

    if args.command == "list":
        records = cf.call(f"/zones/{zone}/dns_records?per_page=200").get("result") or []
        print(f"{'NAME':<44} {'TYPE':<6} {'CONTENT':<50} PROXIED")
        print("-" * 112)
        for rec in sorted(records, key=lambda r: r["name"]):
            print(f"{rec['name']:<44} {rec['type']:<6} "
                  f"{str(rec['content'])[:48]:<50} {rec.get('proxied')}")
        return

    if args.command == "show":
        records = cf.call(
            f"/zones/{zone}/dns_records?name={args.hostname}").get("result") or []
        if not records:
            print(f"no record for {args.hostname}")
            return
        for rec in records:
            print(f"  {rec['id']} {rec['type']} {rec['content']} "
                  f"proxied={rec.get('proxied')} ttl={rec.get('ttl')}")

    if args.command == "point":
        existing = cf.call(
            f"/zones/{zone}/dns_records?name={args.hostname}").get("result") or []
        for rec in existing:
            print(f"current: {rec['type']} {rec['content']} proxied={rec.get('proxied')}")

        body = {"type": "A", "name": args.hostname, "content": args.origin_ip,
                "ttl": 1, "proxied": True, "comment": "Nekomari panel"}
        if args.dry_run:
            print(f"would set: A {args.hostname} -> {args.origin_ip} (proxied)")
            return

        if existing:
            res = cf.call(f"/zones/{zone}/dns_records/{existing[0]['id']}", "PUT", body)
        else:
            res = cf.call(f"/zones/{zone}/dns_records", "POST", body)
        if not res.get("success"):
            raise SystemExit(f"failed: {res.get('errors')}")
        rec = res["result"]
        print(f"set: {rec['type']} {rec['name']} {rec['content']} proxied={rec.get('proxied')}")
        print("\nRemember: HTTP/HTTPS through Cloudflare needs an edge certificate "
              "covering this hostname. Universal SSL covers only `*.zone` (one "
              "label), so a 2nd-level name needs Total TLS or an advanced "
              "certificate, which this DNS-scoped token cannot configure.")


if __name__ == "__main__":
    sys.exit(main())
