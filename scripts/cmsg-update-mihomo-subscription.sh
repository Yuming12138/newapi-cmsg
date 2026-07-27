#!/usr/bin/env bash
set -euo pipefail

cd /opt/mihomo
umask 077

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
    print "external-controller: 127.0.0.1:9090"
  }
  /^(mixed-port|port|socks-port|redir-port|tproxy-port|allow-lan|bind-address|mode|log-level|external-controller|external-ui|secret|ipv6):/ { next }
  { print }
' "$tmp" > "$tmp.config"

python3 - "$tmp.config" <<'PYOPENAI'
from pathlib import Path
import sys

path = Path(sys.argv[1])

# Keep ChatGPT traffic on a dedicated fallback group. The node order is
# deliberate: SG1 is the current primary, US2 remains the first fallback,
# and SG2 is retained as a last resort until its connectivity is repaired.
nodes = [
    "🇸🇬 新加坡1 (移动联通>电信)",
    "🇺🇸 美国2",
    "🇸🇬 新加坡2 (移动联通>电信)",
]

def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"

group_line = (
    "    - { name: OpenAI稳定, type: fallback, proxies: ["
    + ", ".join(yaml_quote(node) for node in nodes)
    + "], url: 'https://chatgpt.com/backend-api/codex/responses', "
      "interval: 30, timeout: 5000, max-failed-times: 2, lazy: false }"
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

# A manual fallback selection is persisted in cache.db by default. Clear it
# after restart so OpenAI稳定 keeps selecting the first healthy node instead
# of remaining pinned to the previously selected node.
cleared_fixed=0
for _ in $(seq 1 50); do
  container_pid=$(docker inspect -f '{{.State.Pid}}' mihomo 2>/dev/null || true)
  if [[ "$container_pid" =~ ^[0-9]+$ ]] &&
    nsenter -t "$container_pid" -n curl -fsS --max-time 2 -X DELETE \
      'http://127.0.0.1:9090/proxies/OpenAI%E7%A8%B3%E5%AE%9A' >/dev/null 2>&1; then
    cleared_fixed=1
    break
  fi
  sleep 0.1
done
if [[ "$cleared_fixed" != "1" ]]; then
  echo "failed to clear persisted OpenAI fallback selection" >&2
  exit 1
fi
