#!/usr/bin/env bash
set -euo pipefail

health_url="${CMSG_HOME_WATCHDOG_HEALTH_URL:-http://127.0.0.1:8317/healthz}"
listen_port="${CMSG_HOME_WATCHDOG_LISTEN_PORT:-18327}"
failure_threshold="${CMSG_HOME_WATCHDOG_FAILURE_THRESHOLD:-3}"
state_file="${CMSG_HOME_WATCHDOG_STATE_FILE:-/run/cmsg-home-tunnel-watchdog.failures}"

log_message() {
  /usr/bin/logger -t cmsg-home-tunnel-watchdog -- "$*" || true
}

reset_failures() {
  rm -f -- "$state_file"
}

if [[ ! "$listen_port" =~ ^[0-9]+$ ]] || ((listen_port < 1 || listen_port > 65535)); then
  log_message "invalid listener port: $listen_port"
  exit 2
fi
if [[ ! "$failure_threshold" =~ ^[0-9]+$ ]] || ((failure_threshold < 1)); then
  log_message "invalid failure threshold: $failure_threshold"
  exit 2
fi

status=$(/usr/bin/curl --silent --output /dev/null --write-out '%{http_code}' --max-time 3 "$health_url" || true)
case "$status" in
  200)
    reset_failures
    exit 0
    ;;
  503)
    ;;
  *)
    # CPA restarts and unrelated local failures must not tear down a healthy
    # reverse tunnel. Only the Home heartbeat middleware's 503 is actionable.
    reset_failures
    exit 0
    ;;
esac

failures=0
if [[ -f "$state_file" ]]; then
  read -r failures < "$state_file" || failures=0
fi
if [[ ! "$failures" =~ ^[0-9]+$ ]]; then
  failures=0
fi
((failures += 1))
if ((failures > failure_threshold)); then
  failures=$failure_threshold
fi
printf '%s\n' "$failures" > "${state_file}.tmp"
mv -f -- "${state_file}.tmp" "$state_file"

if ((failures < failure_threshold)); then
  exit 0
fi

listener_output=$(/usr/bin/ss -H -ltnp "sport = :${listen_port}" || true)
if ! /usr/bin/grep -Fq "127.0.0.1:${listen_port}" <<< "$listener_output"; then
  log_message "health returned 503 $failures times but the expected loopback listener is absent"
  exit 1
fi

mapfile -t listener_pids < <(
  /usr/bin/grep -oE 'pid=[0-9]+' <<< "$listener_output" |
    /usr/bin/cut -d= -f2 |
    /usr/bin/sort -u
)
if ((${#listener_pids[@]} != 1)); then
  log_message "refusing recovery: expected exactly one listener PID, found ${#listener_pids[@]}"
  exit 1
fi

listener_pid="${listener_pids[0]}"
listener_comm=$(/usr/bin/ps -p "$listener_pid" -o comm= | /usr/bin/xargs || true)
listener_args=$(/usr/bin/ps -p "$listener_pid" -o args= || true)
if [[ "$listener_comm" != "sshd" || "$listener_args" != "sshd: cmsg-tunnel"* ]]; then
  log_message "refusing recovery: listener PID $listener_pid is not the cmsg-tunnel sshd session"
  exit 1
fi

log_message "Home heartbeat failed $failures consecutive checks; terminating listener PID $listener_pid for reconnect"
/bin/kill -TERM -- "$listener_pid"
reset_failures
