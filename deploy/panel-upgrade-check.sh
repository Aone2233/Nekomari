#!/usr/bin/env bash
# Post-upgrade self-check for the panel: the "Verification" row of every rollout in
# docs/DEPLOY-OC424.md, in one command.
#
# Why this exists: after each panel upgrade the same eight things were checked by hand --
# container health and restart count, /api/version against the tag that was supposed to
# ship, /api/public, the front page, PRAGMA quick_check, the pre-upgrade archives, and the
# ERROR count in the first five minutes. Doing that by hand is what let two regressions get
# past a rollout (the v1.6.16 restart loop was "the container is up" until it was not, and a
# freelist mismatch in quick_check looked like a detail until it blocked a start). A script
# makes the row reproducible and makes "0 errors" a number rather than an impression.
#
# Read-only by construction: GET requests, docker inspect / docker logs, and PRAGMA
# quick_check on a read-only connection. Nothing is written, restarted or deleted -- in
# particular the upgrade archives under data/backup are never touched, because they are the
# rollback path.
#
# Usage:
#   EXPECTED_VERSION=v1.6.19 bash deploy/panel-upgrade-check.sh
#   EXPECTED_VERSION=v1.6.19 EXPECTED_HASH=5f4d7fa sudo -E bash deploy/panel-upgrade-check.sh
#
# Environment:
#   EXPECTED_VERSION  required  the tag the panel is supposed to be running (v1.6.19)
#   EXPECTED_HASH     optional  the commit /api/version should report (5f4d7fa); skipped when unset
#   BASE_URL          default   http://127.0.0.1:25774
#   DATA_DIR          default   /opt/nekomari/data
#   CONTAINER         default   nekomari
#   MAX_ERRORS        default   0     ERROR lines allowed in the log window
#   MAX_RESTARTS      default   0     container restarts allowed
#   LOG_WINDOW        default   5m    docker logs --since value
#   HTTP_TIMEOUT      default   10    curl --max-time, seconds
#   BACKUP_WARN_MB    default   512   warn (not fail) above this total in data/backup
#   DOCKER_BIN        default   docker      the docker CLI to read container state and logs with
#   SQLITE_BIN        default   sqlite3     the sqlite3 CLI used for PRAGMA quick_check
#   (DOCKER_BIN / SQLITE_BIN exist so the offline test can point at a stub, or at a path that
#    does not exist to pin the "tool is missing -> SKIP" branch; they are also how a host with
#    docker under a different name would be handled.)
#
# Exit codes: 0 every check passed (SKIP and WARN are not failures), 1 a check failed,
# 2 configuration error -- nothing was checked in that case.
#
# Run it within five minutes of the upgrade finishing: the log window is "the last LOG_WINDOW"
# measured from now, not from the upgrade.
set -uo pipefail

EXPECTED_VERSION="${EXPECTED_VERSION:-}"
EXPECTED_HASH="${EXPECTED_HASH:-}"
BASE_URL="${BASE_URL:-http://127.0.0.1:25774}"
DATA_DIR="${DATA_DIR:-/opt/nekomari/data}"
CONTAINER="${CONTAINER:-nekomari}"
MAX_ERRORS="${MAX_ERRORS:-0}"
MAX_RESTARTS="${MAX_RESTARTS:-0}"
LOG_WINDOW="${LOG_WINDOW:-5m}"
HTTP_TIMEOUT="${HTTP_TIMEOUT:-10}"
BACKUP_WARN_MB="${BACKUP_WARN_MB:-512}"
DOCKER_BIN="${DOCKER_BIN:-docker}"
SQLITE_BIN="${SQLITE_BIN:-sqlite3}"

usage() {
  # Everything between the shebang and the first non-comment line, un-commented.
  awk 'NR > 1 && /^set -uo pipefail/ { exit } NR > 1' "$0" | sed 's/^# \{0,1\}//'
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  "")        ;;
  *)         echo "unknown argument: $1 (this script is configured by environment variables; see --help)" >&2; exit 2 ;;
esac

if [ -z "$EXPECTED_VERSION" ]; then
  echo "EXPECTED_VERSION is not set; refusing to check without knowing which version to expect." >&2
  echo "  e.g. EXPECTED_VERSION=v1.6.19 bash $0" >&2
  usage >&2
  exit 2
fi

# A non-numeric limit would make the comparisons below silently pass or blow up mid-run;
# both read as "checked" while nothing was compared.
for spec in "MAX_ERRORS:$MAX_ERRORS" "MAX_RESTARTS:$MAX_RESTARTS" "HTTP_TIMEOUT:$HTTP_TIMEOUT" "BACKUP_WARN_MB:$BACKUP_WARN_MB"; do
  name="${spec%%:*}"
  value="${spec#*:}"
  case "$value" in
    ''|*[!0-9]*) echo "$name must be a non-negative integer (got '$value')" >&2; exit 2 ;;
  esac
done

for tool in curl; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool missing: $tool" >&2
    exit 2
  fi
done

tmp=$(mktemp -d) || { echo "cannot create a work directory" >&2; exit 2; }
trap 'rm -rf "$tmp"' EXIT

pass=0; fail=0; skipped=0
ok()      { pass=$((pass + 1));    printf '  PASS  %s\n' "$*"; }
bad()     { fail=$((fail + 1));    printf '  FAIL  %s\n' "$*"; }
skip()    { skipped=$((skipped + 1)); printf '  SKIP  %s\n' "$*"; }
warn()    { printf '  WARN  %s\n' "$*"; }
note()    { printf '        %s\n' "$*"; }

first_line() { head -1 "$1" 2>/dev/null | tr -d '\r'; }

# trim <string> -- leading and trailing whitespace removed.
trim() { printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'; }

# version_key <tag> -- the comparable part of a version: "v1.6.19" and "1.6.19" are the same
# release (the tag carries the v, the binary reports the tag, and a hand-typed argument may
# carry either). Nothing else is normalised: v1.6.1 and v1.6.10 must stay different.
version_key() { printf '%s' "$(trim "$1")" | sed 's/^v//'; }

# get <url> <body-file>
#
# Sets HTTP_CODE and HTTP_TYPE. -f is deliberately not used: the status is the diagnosis, and
# collapsing a 502 into "curl exit 22" throws away the one fact that says whether the panel or
# the proxy in front of it answered. --max-time keeps a hung panel from hanging the check.
get() {
  local meta
  meta=$(curl -sS -o "$2" -w '%{http_code} %{content_type}' --max-time "$HTTP_TIMEOUT" "$1" 2>"$tmp/curl.err")
  HTTP_CODE="${meta%% *}"
  HTTP_TYPE="${meta#* }"
  [ -n "$HTTP_CODE" ] || HTTP_CODE=000
}

# json_string <file> <key> -- the first string value of <key> in a JSON body, or "".
#
# sed rather than jq: this has to run on the panel host, where the guarantee is
# curl/coreutils/sed/awk only (same reasoning as deploy/panel-probe.sh).
json_string() {
  sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" 2>/dev/null | head -1
}

envelope_ok() {
  grep -Eq '"status"[[:space:]]*:[[:space:]]*"success"' "$1" 2>/dev/null
}

printf 'panel-upgrade-check: expecting %s at %s (data %s, container %s)\n\n' \
  "$EXPECTED_VERSION" "$BASE_URL" "$DATA_DIR" "$CONTAINER"

# ---- 1. the container -----------------------------------------------------------------
# A crash-loop is the v1.6.16 shape: "docker ps" shows Up, the healthcheck flips between
# starting and unhealthy, and RestartCount keeps climbing. One inspect gives both numbers.
docker_state=""
docker_restarts=""
if ! command -v "$DOCKER_BIN" >/dev/null 2>&1; then
  skip "container $CONTAINER: $DOCKER_BIN is not installed, so its state cannot be read"
  skip "container restarts: $DOCKER_BIN is not installed"
else
  if "$DOCKER_BIN" inspect \
       --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}|{{.RestartCount}}' \
       "$CONTAINER" >"$tmp/inspect.txt" 2>"$tmp/inspect.err"; then
    docker_state=$(sed -n 's/|.*//p' "$tmp/inspect.txt" | head -1)
    docker_restarts=$(sed -n 's/.*|//p' "$tmp/inspect.txt" | head -1)
  else
    bad "container $CONTAINER: docker inspect failed: $(first_line "$tmp/inspect.err")"
  fi

  case "$docker_state" in
    healthy) ok "container $CONTAINER is healthy" ;;
    running) ok "container $CONTAINER is running (no healthcheck configured)" ;;
    starting) bad "container $CONTAINER health is 'starting': the upgrade has not settled yet, re-run in a minute" ;;
    unhealthy) bad "container $CONTAINER is unhealthy" ;;
    "") : ;; # inspect already failed; the FAIL above says why
    *) bad "container $CONTAINER state is '$docker_state'" ;;
  esac

  if [ -n "$docker_restarts" ]; then
    case "$docker_restarts" in
      ''|*[!0-9]*) bad "container $CONTAINER restart count is not a number ('$docker_restarts')" ;;
      *)
        if [ "$docker_restarts" -gt "$MAX_RESTARTS" ]; then
          bad "container $CONTAINER restarted $docker_restarts time(s) (limit $MAX_RESTARTS): a crash loop looks exactly like this"
        else
          ok "container $CONTAINER restarted $docker_restarts time(s) (limit $MAX_RESTARTS)"
        fi
        ;;
    esac
  fi
fi

# ---- 2. /api/version ------------------------------------------------------------------
# The binary reports utils.CurrentVersion, which release.yml injects from the tag -- so this
# is the check that the upgrade actually replaced the binary rather than only the tag on the
# compose line.
get "$BASE_URL/api/version" "$tmp/version.json"
if [ "$HTTP_CODE" = 000 ]; then
  bad "/api/version: no answer from $BASE_URL ($(first_line "$tmp/curl.err"))"
elif [ "$HTTP_CODE" != 200 ]; then
  bad "/api/version: HTTP $HTTP_CODE"
elif ! envelope_ok "$tmp/version.json"; then
  bad "/api/version: not the panel's success envelope: $(head -c 120 "$tmp/version.json")"
else
  reported_version=$(json_string "$tmp/version.json" version)
  reported_hash=$(json_string "$tmp/version.json" hash)
  if [ -z "$reported_version" ]; then
    bad "/api/version: the envelope carries no version field"
  elif [ "$(version_key "$reported_version")" != "$(version_key "$EXPECTED_VERSION")" ]; then
    bad "/api/version reports $reported_version, expected $EXPECTED_VERSION: the running binary is not the release that was supposed to ship"
  else
    ok "/api/version reports $reported_version (expected $EXPECTED_VERSION), hash $reported_hash"
  fi

  if [ -z "$EXPECTED_HASH" ]; then
    skip "/api/version hash: EXPECTED_HASH is not set, so the commit was not checked"
  else
    case "$reported_hash" in
      ""|unknown)
        bad "/api/version hash is '$reported_hash': this binary was not built by the release workflow"
        ;;
      *)
        case "$reported_hash" in
          "$EXPECTED_HASH"*) ok "/api/version hash $reported_hash matches $EXPECTED_HASH" ;;
          *) bad "/api/version hash is $reported_hash, expected $EXPECTED_HASH" ;;
        esac
        ;;
    esac
  fi
fi

# ---- 3. /api/public and the front page ------------------------------------------------
get "$BASE_URL/api/public" "$tmp/public.json"
if [ "$HTTP_CODE" = 000 ]; then
  bad "/api/public: no answer from $BASE_URL ($(first_line "$tmp/curl.err"))"
elif [ "$HTTP_CODE" != 200 ]; then
  bad "/api/public: HTTP $HTTP_CODE"
elif ! envelope_ok "$tmp/public.json"; then
  bad "/api/public: 200 but not a success envelope (an edge error page looks like this)"
else
  ok "/api/public: 200 with a success envelope"
fi

get "$BASE_URL/" "$tmp/index.html"
case "$HTTP_CODE" in
  000) bad "GET /: no answer from $BASE_URL ($(first_line "$tmp/curl.err"))" ;;
  200)
    case "$HTTP_TYPE" in
      *html*) ok "GET /: 200 ($HTTP_TYPE)" ;;
      *)      bad "GET /: 200 but Content-Type is '$HTTP_TYPE', not an HTML document" ;;
    esac
    ;;
  *) bad "GET /: HTTP $HTTP_CODE" ;;
esac

# ---- 4. the databases -----------------------------------------------------------------
# -readonly is the guarantee that this check cannot disturb the running panel: PRAGMA
# quick_check is a read-only statement, but a read-write open can still create or remove the
# -wal/-shm files next to a live database. Old sqlite3 CLIs have no -readonly; those fall
# back to a normal open (still only running the read-only statement) and say so.
if ! command -v "$SQLITE_BIN" >/dev/null 2>&1; then
  skip "database: $SQLITE_BIN is not installed, so PRAGMA quick_check was not run"
elif [ ! -f "$DATA_DIR/komari.db" ]; then
  bad "database: $DATA_DIR/komari.db does not exist"
else
  db_out=$("$SQLITE_BIN" -readonly "$DATA_DIR/komari.db" "PRAGMA quick_check;" 2>"$tmp/db.err")
  db_rc=$?
  db_mode="read-only"
  if [ "$db_rc" -ne 0 ] && grep -qi 'unknown option' "$tmp/db.err"; then
    db_out=$("$SQLITE_BIN" "$DATA_DIR/komari.db" "PRAGMA quick_check;" 2>"$tmp/db.err")
    db_rc=$?
    db_mode="read-write open, this sqlite3 CLI has no -readonly"
  fi
  if [ "$db_rc" -ne 0 ]; then
    bad "database: sqlite3 exited $db_rc: $(first_line "$tmp/db.err")"
  elif [ "$(printf '%s' "$db_out" | tr -d '[:space:]')" = "ok" ]; then
    ok "database quick_check: ok ($db_mode)"
  else
    bad "database quick_check: $(printf '%s' "$db_out" | head -1)"
    printf '%s\n' "$db_out" | sed -n '2,4p' | sed 's/^/          /'
    note "a freelist accounting mismatch is what blocked the v1.6.16 start; 'sqlite3 komari.db VACUUM;' repaired it (see docs/DEPLOY-OC424.md)"
  fi
fi

# ---- 5. the pre-upgrade archives ------------------------------------------------------
# The server writes data/backup/upgrade-<ts>.zip when it first boots after a version change.
# Zero of them after an upgrade means there is no rollback point -- the one thing that made
# the v1.6.16 rollback possible.
backup_dir="$DATA_DIR/backup"
if [ ! -d "$backup_dir" ]; then
  bad "upgrade archives: $backup_dir does not exist, so no pre-upgrade backup was written"
else
  archive_count=$(find "$backup_dir" -maxdepth 1 -type f -name 'upgrade-*.zip' 2>/dev/null | wc -l | tr -d ' ')
  if [ "$archive_count" = 0 ]; then
    bad "upgrade archives: no upgrade-*.zip in $backup_dir, so this upgrade has no rollback point"
  else
    archive_mb=$(find "$backup_dir" -maxdepth 1 -type f -name 'upgrade-*.zip' -printf '%s\n' 2>/dev/null \
                 | awk '{ total += $1 } END { printf "%.1f", (total + 0) / 1048576 }')
    [ -n "$archive_mb" ] || archive_mb="?"
    archive_newest=$(find "$backup_dir" -maxdepth 1 -type f -name 'upgrade-*.zip' -printf '%T@ %f\n' 2>/dev/null \
                     | sort -rn | head -1 | cut -d' ' -f2-)
    ok "upgrade archives: $archive_count file(s), $archive_mb MB, newest $archive_newest"
    if [ "$archive_mb" != "?" ] && awk -v mb="$archive_mb" -v limit="$BACKUP_WARN_MB" 'BEGIN { exit !(mb + 0 > limit + 0) }'; then
      warn "data/backup is $archive_mb MB (above $BACKUP_WARN_MB MB); deploy/prune-upgrade-backups.sh keeps the newest 3"
    fi
  fi
fi

# ---- 6. the log window ----------------------------------------------------------------
# [ERROR/<component>] is what utils/log's console handler prints (level name inside brackets).
# level=ERROR is the structured form, kept in case a future handler changes the console shape.
if ! command -v "$DOCKER_BIN" >/dev/null 2>&1; then
  skip "logs: $DOCKER_BIN is not installed, so no log window could be read"
else
  if "$DOCKER_BIN" logs --since "$LOG_WINDOW" "$CONTAINER" >"$tmp/logs.txt" 2>"$tmp/logs.err"; then
    error_count=$(grep -c -E '\[ERROR/|level=ERROR' "$tmp/logs.txt" 2>/dev/null)
    [ -n "$error_count" ] || error_count=0
    if [ "$error_count" -gt "$MAX_ERRORS" ]; then
      bad "logs: $error_count ERROR line(s) in the last $LOG_WINDOW (limit $MAX_ERRORS)"
      grep -E '\[ERROR/|level=ERROR' "$tmp/logs.txt" | head -3 | sed 's/^/          /'
    else
      ok "logs: $error_count ERROR line(s) in the last $LOG_WINDOW (limit $MAX_ERRORS)"
    fi
  else
    bad "logs: docker logs failed: $(first_line "$tmp/logs.err")"
  fi
fi

# ---- summary ------------------------------------------------------------------------
printf '\n===== %d passed, %d failed, %d skipped =====\n' "$pass" "$fail" "$skipped"
if [ "$fail" -gt 0 ]; then
  printf 'A failed check is a reason to roll back, not a reason to keep going: the pre-upgrade\n'
  printf 'compose line is in docs/DEPLOY-OC424.md for this rollout, the data directory is not\n'
  printf 'touched by an image rollback, and the archive this check just counted is the backup.\n'
  exit 1
fi
printf 'no failures; the release notes and the rollout table in docs/DEPLOY-OC424.md can be\n'
printf 'filled in from the lines above.\n'
exit 0
