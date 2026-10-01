#!/usr/bin/env bash
set -Eeuo pipefail
stage=/tmp/nekomari-v1.6.4-20260930
root=/opt/nekomari
image="$1"
expected="$2"
[[ "$image" == ghcr.io/aone2233/nekomari:v1.6.4@sha256:* ]]
[[ "$(sha256sum "$stage/nekomari-linux-arm64" | cut -d' ' -f1)" == "$expected" ]]
[[ "$(docker image inspect "$image" --format '{{.Architecture}}')" == arm64 ]]
cid=$(docker create "$image")
trap 'docker rm "$cid" >/dev/null 2>&1 || true' EXIT
docker cp "$cid:/app/nekomari" "$stage/image-nekomari"
[[ "$(sha256sum "$stage/image-nekomari" | cut -d' ' -f1)" == "$expected" ]]
docker rm "$cid" >/dev/null
trap - EXIT
cd "$root"
[[ $(grep -c '^    image: ghcr.io/aone2233/nekomari:v1.6.3$' docker-compose.yml) == 1 ]]
backup="$root/backups/v1.6.3-before-v1.6.4-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -m 700 -p "$backup"
cp -a docker-compose.yml "$backup/docker-compose.yml"
rollback() {
  local rc=$?
  trap - ERR
  cp -a "$backup/docker-compose.yml" "$root/docker-compose.yml"
  docker compose up -d || true
  printf 'FAILED; previous Compose restored; backup=%s; exit=%s\n' "$backup" "$rc" >&2
  exit "$rc"
}
trap rollback ERR
docker compose stop nekomari
tar --xattrs --acls -czf "$backup/full-data-and-compose.tar.gz" docker-compose.yml data
tar -tzf "$backup/full-data-and-compose.tar.gz" > "$backup/manifest.txt"
grep -qx 'data/komari.db' "$backup/manifest.txt"
grep -qx 'data/metrics.db' "$backup/manifest.txt"
(cd "$backup"; sha256sum full-data-and-compose.tar.gz > SHA256SUMS; sha256sum -c SHA256SUMS)
python3 - <<'PY'
import sqlite3
for name in ['komari.db', 'metrics.db']:
    db = sqlite3.connect('file:/opt/nekomari/data/' + name + '?mode=ro', uri=True)
    result = db.execute('PRAGMA quick_check').fetchall()
    assert result == [('ok',)], (name, result)
    print(name, 'quick_check=ok')
    db.close()
PY
sed -i "s|^    image: ghcr.io/aone2233/nekomari:v1.6.3$|    image: $image|" docker-compose.yml
docker compose config --quiet
docker compose up -d nekomari
for n in $(seq 1 36); do
  state=$(docker inspect nekomari --format '{{.State.Health.Status}}')
  [[ "$state" == healthy ]] && break
  sleep 5
done
[[ "$state" == healthy ]]
[[ "$(docker inspect nekomari --format '{{.RestartCount}}')" == 0 ]]
curl --fail --silent http://127.0.0.1:25774/api/version
printf '\nbackup=%s\n' "$backup"
cat "$backup/SHA256SUMS"
trap - ERR
