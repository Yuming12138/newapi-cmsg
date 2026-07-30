#!/usr/bin/env bash
set -euo pipefail

cd /opt/mihomo
umask 077

controller_secret_file=${CMSG_MIHOMO_CONTROLLER_SECRET_FILE:-/opt/cliproxyapi/secrets/mihomo-controller}
if [[ ! -s "$controller_secret_file" ]]; then
  echo "missing Mihomo controller secret: $controller_secret_file" >&2
  exit 1
fi
preferred_node='🇯🇵 日本1 (移动>电信>联通)'

url=$(cat subscription.url)
tmp=$(mktemp /opt/mihomo/subscription.XXXXXX)
trap 'rm -f "$tmp" "$tmp.config"' EXIT

curl -fsSL --connect-timeout 15 --max-time 90 -A 'Clash.Meta' "$url" -o "$tmp"
awk '
  BEGIN {
    print "mixed-port: 7890"
    print "socks-port: 1080"
    print "allow-lan: true"
    print "bind-address: 0.0.0.0"
    print "mode: rule"
    print "log-level: warning"
    print "external-controller: 0.0.0.0:9090"
  }
  /^(mixed-port|port|socks-port|redir-port|tproxy-port|allow-lan|bind-address|mode|log-level|external-controller|external-ui|secret|ipv6):/ { next }
  { print }
' "$tmp" > "$tmp.config"

python3 - "$tmp.config" "$controller_secret_file" <<'PYOPENAI'
from pathlib import Path
import json
import sys

path = Path(sys.argv[1])
controller_secret = Path(sys.argv[2]).read_text(encoding="utf-8").strip()
if not controller_secret:
    raise SystemExit("Mihomo controller secret is empty")

# CPA owns failover and cooldown policy for this select group. The node order is
# based on Codex endpoint probes and production dial-failure history.
nodes = [
    "🇯🇵 日本1 (移动>电信>联通)",
    "🇺🇸 美国1",
    "🇸🇬 新加坡1 (移动联通>电信)",
]

def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"

group_line = (
    "    - { name: OpenAI稳定, type: select, proxies: ["
    + ", ".join(yaml_quote(node) for node in nodes)
    + "] }"
)
rule_lines = [
    "    - 'DOMAIN-SUFFIX,chatgpt.com,OpenAI稳定'",
    "    - 'DOMAIN-SUFFIX,openai.com,OpenAI稳定'",
    "    - 'DOMAIN-SUFFIX,oaistatic.com,OpenAI稳定'",
    "    - 'DOMAIN-SUFFIX,oaiusercontent.com,OpenAI稳定'",
]

lines = [ln for ln in path.read_text(encoding="utf-8").splitlines() if "OpenAI稳定" not in ln]
out = []
inserted_group = inserted_rules = False
for line in lines:
    out.append(line)
    if line.startswith("external-controller:"):
        out.append("secret: " + json.dumps(controller_secret))
    if line.strip() == "proxy-groups:" and not inserted_group:
        out.append(group_line)
        inserted_group = True
    if line.strip() == "rules:" and not inserted_rules:
        out.extend(rule_lines)
        inserted_rules = True

if not inserted_group or not inserted_rules:
    raise SystemExit("failed to inject OpenAI stable routing")

path.write_text("\n".join(out) + "\n", encoding="utf-8")
PYOPENAI

/usr/local/bin/mihomo -t -d /opt/mihomo -f "$tmp.config" >/dev/null
if [[ "${CMSG_MIHOMO_DRY_RUN:-0}" == "1" ]]; then
  grep -n 'OpenAI稳定' "$tmp.config"
  echo "mihomo config validation passed"
  exit 0
fi
install -m 0600 "$tmp" /opt/mihomo/subscription.yaml
install -m 0600 "$tmp.config" /opt/mihomo/config.yaml
docker compose -f /opt/mihomo/docker-compose.yml restart mihomo >/dev/null

# Select the measured preferred node after restart. Later changes are owned by
# CPA's route-recovery coordinator and must not be overridden by Mihomo.
controller_secret=$(cat "$controller_secret_file")
selection_payload=$(python3 - "$preferred_node" <<'PYSELECTION'
import json
import sys
print(json.dumps({"name": sys.argv[1]}, ensure_ascii=False))
PYSELECTION
)
selected_preferred=0
for _ in $(seq 1 50); do
  container_pid=$(docker inspect -f '{{.State.Pid}}' mihomo 2>/dev/null || true)
  if [[ "$container_pid" =~ ^[0-9]+$ ]] &&
    nsenter -t "$container_pid" -n curl -fsS --max-time 2 -X PUT \
      -H "Authorization: Bearer $controller_secret" \
      -H 'Content-Type: application/json' \
      --data "$selection_payload" \
      'http://127.0.0.1:9090/proxies/OpenAI%E7%A8%B3%E5%AE%9A' >/dev/null 2>&1; then
    selected_preferred=1
    break
  fi
  sleep 0.1
done
if [[ "$selected_preferred" != "1" ]]; then
  echo "failed to select preferred OpenAI node" >&2
  exit 1
fi
