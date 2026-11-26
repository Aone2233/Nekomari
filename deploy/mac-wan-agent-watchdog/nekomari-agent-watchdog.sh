#!/usr/bin/env bash
# Monitor the system Agent's outgoing TLS connection, not another Agent PID.
set -uo pipefail

unit=nekomari-agent.service
tag=nekomari-system-watchdog

if (( EUID != 0 )); then
    echo 'Root privileges are required to inspect Agent-owned sockets' >&2
    exit 2
fi

connected() {
    local state pid sockets
    state=$(systemctl show --property=ActiveState --value "$unit") || return 2
    [[ "$state" == active ]] || return 1
    pid=$(systemctl show --property=MainPID --value "$unit") || return 2
    [[ "$pid" =~ ^[0-9]+$ ]] || return 2
    (( pid > 0 )) || return 1
    sockets=$(ss -Htnp state established '( dport = :443 )') || return 2
    grep -Fq "pid=$pid," <<<"$sockets"
}

if [[ ${1:-} == --check && $# == 1 ]]; then
    connected
    exit $?
fi
if (( $# != 0 )); then
    echo 'Usage: nekomari-agent-watchdog.sh [--check]' >&2
    exit 2
fi

exec 9>"${NEKOMARI_AGENT_WATCHDOG_LOCK_FILE:-/run/nekomari-agent-watchdog.lock}"
flock -n 9 || exit 0

if connected; then exit 0; else status=$?; fi
if (( status != 1 )); then
    logger -t "$tag" 'Cannot inspect Agent connection; refusing to restart'
    exit 1
fi
sleep 30
if connected; then exit 0; else status=$?; fi
if (( status != 1 )); then
    logger -t "$tag" 'Cannot recheck Agent connection; refusing to restart'
    exit 1
fi

logger -t "$tag" 'System Agent has no established TLS connection; restarting'
if ! systemctl restart "$unit"; then
    logger -t "$tag" 'System Agent restart failed'
    exit 1
fi
sleep 8
if connected; then
    logger -t "$tag" 'System Agent connection recovered after restart'
    exit 0
else
    logger -t "$tag" 'System Agent connection not verified after restart'
    exit 1
fi
