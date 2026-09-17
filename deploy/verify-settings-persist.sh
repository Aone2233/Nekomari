#!/usr/bin/env bash
# Prove that an image upgrade preserves settings, using a copy of production data.
#
# The concern: a user updating through the official channel (pull a new image,
# recreate the container) must not lose their configuration. The data volume is a
# bind mount, so it survives by construction -- but "should survive" is not
# evidence, and a migration or init path could still overwrite something.
#
# So this does a real upgrade cycle against a throwaway copy:
#   1. run the PREVIOUS image against the copy, note every setting
#   2. run the CURRENT image against the same copy (a genuine version change)
#   3. compare the settings
# Production is never touched: separate data directory, separate port.
#
# Usage: verify-settings-persist.sh <old-image> <new-image> [port]
set -uo pipefail

OLD="${1:?usage: verify-settings-persist.sh <old-image> <new-image> [port]}"
NEW="${2:?}"
PORT="${3:-25899}"
WORK=/tmp/settings-persist-$$
DATA="$WORK/data"

cleanup() {
  docker rm -f settings-persist >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "== copying production data (read-only source) =="
sudo mkdir -p "$DATA"
sudo cp -a /opt/nekomari/data/. "$DATA"/ 2>/dev/null
# The upgrade backups are large and irrelevant here.
sudo rm -rf "$DATA/backup"
sudo chown -R "$(id -u):$(id -g)" "$WORK" 2>/dev/null
du -sh "$DATA" | sed 's/^/  /'

start() {
  docker rm -f settings-persist >/dev/null 2>&1
  docker run -d --name settings-persist \
    -p "127.0.0.1:${PORT}:25774" \
    -v "$DATA:/app/data" \
    -e TZ=Asia/Shanghai \
    -e KOMARI_LISTEN=0.0.0.0:25774 \
    "$1" >/dev/null
  for _ in $(seq 1 40); do
    sleep 1
    curl -fsS "http://127.0.0.1:${PORT}/api/public" >/dev/null 2>&1 && return 0
  done
  echo "  container never became ready:"
  docker logs settings-persist 2>&1 | tail -5 | sed 's/^/    /'
  return 1
}

snapshot() {
  # Settings that live in the database and on disk, i.e. everything a user sets.
  python3 - "$PORT" <<'PY'
import hashlib, json, sys, urllib.request
port = sys.argv[1]
B = f"http://127.0.0.1:{port}"

def get(p):
    req = urllib.request.Request(B + p)
    req.add_header("User-Agent", "persist-check")
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read()

pub = json.loads(get("/api/public"))["data"]
keep = {k: pub.get(k) for k in
        ("sitename", "description", "theme", "private_site", "record_enabled",
         "ping_record_preserve_time", "custom_head", "custom_body",
         "disable_password_login", "oauth_enable")}
keep["theme_settings_keys"] = sorted((pub.get("theme_settings") or {}).keys())
try:
    fav = get("/favicon.ico")
    keep["favicon_sha256"] = hashlib.sha256(fav).hexdigest()[:16]
    keep["favicon_bytes"] = len(fav)
except Exception as e:
    keep["favicon"] = f"error: {type(e).__name__}"
print(json.dumps(keep, ensure_ascii=False, sort_keys=True))
PY
}

echo
echo "== 1. run the OLD image ($OLD) =="
start "$OLD" || exit 1
OLD_SNAP=$(snapshot)
echo "$OLD_SNAP" | python3 -m json.tool | sed 's/^/  /'

echo
echo "== 2. run the NEW image against the same data ($NEW) =="
start "$NEW" || exit 1
NEW_SNAP=$(snapshot)

echo
echo "== 3. compare =="
python3 - "$OLD_SNAP" "$NEW_SNAP" <<'PY'
import json, sys
a, b = json.loads(sys.argv[1]), json.loads(sys.argv[2])
keys = sorted(set(a) | set(b))
bad = 0
for k in keys:
    if a.get(k) != b.get(k):
        print(f"  CHANGED  {k}\n      before: {a.get(k)}\n      after : {b.get(k)}")
        bad += 1
if bad:
    print(f"\n  {bad} setting(s) changed across the upgrade")
    sys.exit(1)
print(f"  all {len(keys)} settings identical across the upgrade")
PY

echo
echo "== 4. did the upgrade take a backup first? =="
ls -1 "$DATA/backup" 2>/dev/null | sed 's/^/  /' || echo "  (no backup dir)"
echo
echo "== 5. version marker =="
python3 - "$DATA/komari.db" <<'PY'
import sqlite3, sys
try:
    c = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
    for k, v in c.execute("select key, value from configs where key like '%version%'"):
        print(f"  {k} = {v}")
except Exception as e:
    print(f"  could not read: {e}")
PY
