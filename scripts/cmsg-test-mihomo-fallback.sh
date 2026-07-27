#!/usr/bin/env bash
set -euo pipefail

mihomo_bin="${MIHOMO_BIN:-/usr/local/bin/mihomo}"
mixed_port="${MIHOMO_TEST_MIXED_PORT:-17890}"
controller_port="${MIHOMO_TEST_CONTROLLER_PORT:-19090}"
work_dir=$(mktemp -d)
pid=""

cleanup() {
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid"
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

cat > "$work_dir/config.yaml" <<YAML
mixed-port: $mixed_port
allow-lan: false
mode: rule
log-level: warning
external-controller: 127.0.0.1:$controller_port
proxies:
  - name: Broken
    type: socks5
    server: 127.0.0.1
    port: 1
proxy-groups:
  - name: TestFallback
    type: fallback
    proxies: [Broken, DIRECT]
    url: http://www.gstatic.com/generate_204
    interval: 2
    timeout: 1000
    max-failed-times: 2
    lazy: false
rules:
  - MATCH,TestFallback
YAML

"$mihomo_bin" -t -d "$work_dir" -f "$work_dir/config.yaml" >/dev/null
"$mihomo_bin" -d "$work_dir" -f "$work_dir/config.yaml" >"$work_dir/mihomo.log" 2>&1 &
pid=$!

for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:$controller_port/version" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

for _ in $(seq 1 50); do
  state=$(curl -fsS "http://127.0.0.1:$controller_port/proxies/TestFallback")
  now=$(printf '%s' "$state" | python3 -c 'import json,sys; print(json.load(sys.stdin)["now"])')
  fixed=$(printf '%s' "$state" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("fixed", ""))')
  if [[ "$now" == "DIRECT" && -z "$fixed" ]]; then
    break
  fi
  sleep 0.1
done

if [[ "$now" != "DIRECT" || -n "$fixed" ]]; then
  echo "fallback did not select DIRECT: $state" >&2
  cat "$work_dir/mihomo.log" >&2
  exit 1
fi

status=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
  -x "http://127.0.0.1:$mixed_port" http://www.gstatic.com/generate_204)
if [[ "$status" != "204" ]]; then
  echo "fallback request returned HTTP $status, want 204" >&2
  exit 1
fi

echo "fallback smoke test passed: Broken -> DIRECT, status=204"
