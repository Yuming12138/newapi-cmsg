#!/usr/bin/env bash
set -euo pipefail

cd /opt/mihomo
umask 077

controller_secret_file=${CMSG_MIHOMO_CONTROLLER_SECRET_FILE:-/opt/cliproxyapi/secrets/mihomo-controller}
if [[ ! -s "$controller_secret_file" ]]; then
  echo "missing Mihomo controller secret: $controller_secret_file" >&2
  exit 1
fi
url=$(cat subscription.url)
tmp=$(mktemp /opt/mihomo/subscription.XXXXXX)
trap 'rm -f "$tmp" "$tmp.config" "$tmp.preferred"' EXIT

curl -fsSL --connect-timeout 15 --max-time 90 -A 'Clash.Meta' "$url" -o "$tmp"

# The subscription only supplies the proxy node list. Everything else
# (globals, dns, proxy-groups, rules) is templated from the CURRENT
# /opt/mihomo/config.yaml so that a fat airport template (fake-ip DNS,
# GEOSITE rules, ad-block rulesets, its own proxy-groups) can never leak
# into production. Our two routing groups are rebuilt from measured node
# preferences and re-injected on every run.
python3 - "$tmp" /opt/mihomo/config.yaml "$controller_secret_file" "$tmp.preferred" "$tmp.config" <<'PYOPENAI'
from pathlib import Path
import re
import sys

subscription_path = Path(sys.argv[1])
template_path = Path(sys.argv[2])
controller_secret = Path(sys.argv[3]).read_text(encoding="utf-8").strip()
preferred_node_path = Path(sys.argv[4])
output_path = Path(sys.argv[5])
if not controller_secret:
    raise SystemExit("Mihomo controller secret is empty")

# --- 1. Extract proxy node lines from the fresh subscription ----------------
sub_lines = subscription_path.read_text(encoding="utf-8").splitlines()
proxy_lines = []
in_proxies = False
for line in sub_lines:
    if line.strip() == "proxies:":
        in_proxies = True
        continue
    if in_proxies and re.match(r"^[a-zA-Z_-]+:", line):
        break  # next top-level section ends the proxies block
    if in_proxies and line.strip().startswith("- "):
        proxy_lines.append(line)
if len(proxy_lines) < 3:
    raise SystemExit(f"subscription yielded only {len(proxy_lines)} proxy nodes")

# --- 2. Resolve group members by stable name prefixes -----------------------
available_names = []
name_pattern = re.compile(r"\bname:\s*(?:'((?:''|[^'])*)'|\"([^\"]*)\"|([^,}\n]+))")
for line in proxy_lines:
    match = name_pattern.search(line)
    if not match:
        continue
    value = next(group for group in match.groups() if group is not None).strip()
    available_names.append(value.replace("''", "'"))


def try_resolve_node(prefix: str):
    """Best-effort resolve: return the node when exactly one subscription node
    matches the prefix, else None (missing or ambiguous)."""
    matches = [name for name in available_names if name.startswith(prefix)]
    return matches[0] if len(matches) == 1 else None


def resolve_group(prefixes, minimum, group_label):
    nodes = []
    for prefix in prefixes:
        node = try_resolve_node(prefix)
        if node and node not in nodes:
            nodes.append(node)
    if len(nodes) < minimum:
        raise SystemExit(
            f"{group_label}: only resolved {len(nodes)} nodes from subscription, "
            f"need at least {minimum}; subscription layout may have changed"
        )
    return nodes


# CPA owns failover and cooldown policy for the OpenAI fallback group. ASXS
# balance checks use a separate Mihomo fallback group so one dead airport node
# cannot block the periodic usage refresh.
#
# Node order is based on measured Codex endpoint latency plus observed GFW
# interference patterns (2026-09-06 full sweep + evening re-probes against
# https://chatgpt.com/backend-api/codex/responses; 405 means TLS+HTTP
# reachable, 403 means the exit IP is Cloudflare-challenged):
#   - 日本东京 (vless+ws behind Cloudflare CDN): 0.5-1.6s, most resilient to
#     IP-level blocking because Cloudflare front IPs are rarely blocked.
#   - AWS新加坡 (vless+reality): 0.4-0.5s, stable so far.
#   - 新加坡2/3 高速专线-hy2: 0.6-1.7s, UDP-based, good diversity.
#   - AWS日本 (vless+reality): 0.4-0.5s when reachable, but its AWS JP entry
#     IPs get intermittently blocked at night (observed i/o timeouts at
#     23:20 after being clean at 22:45), so they are ordered after the
#     CDN-fronted nodes; fallback still uses them when healthy.
#   - 香港HKT is excluded on purpose: codex is Cloudflare-challenged (403).
#   - Legacy airport nodes (jiedian.stream, e.g. "🇺🇸 美国1") are kept as
#     last-resort entries only; they simply do not resolve once the legacy
#     subscription is gone.
openai_nodes = resolve_group(
    [
        # Proven to hold long-lived low-bandwidth connections (codex SSE
        # profile) during evening peak on 2026-09-06: 东京06/09 and
        # AWS新加坡02/03 completed 25s-hold tests 2/2 while others flapped.
        "🇯🇵日本东京06", "🇯🇵日本东京09",
        "🇸🇬AWS新加坡02", "🇸🇬AWS新加坡03", "🇸🇬AWS新加坡04", "🇸🇬AWS新加坡05",
        "🇯🇵日本东京01", "🇯🇵日本东京03", "🇯🇵日本东京04", "🇯🇵日本东京05",
        "🇸🇬新加坡2", "🇸🇬新加坡3",
        "🇯🇵AWS日本01", "🇯🇵AWS日本02", "🇯🇵AWS日本03", "🇯🇵AWS日本04",
        "🇯🇵AWS日本05", "🇯🇵AWS日本06", "🇯🇵AWS日本07",
        "🇺🇸 美国1",
    ],
    minimum=3,
    group_label="OpenAI稳定",
)
# ASXS: verified 200 on https://api.asxs.top/ (2026-09-06 sweep). The
# "-0.1倍" / trailing-space prefixes disambiguate same-numbered US series.
asxs_nodes = resolve_group(
    [
        "🇺🇸美国01-0.1倍", "🇺🇸美国02-0.1倍", "🇺🇸美国03-0.1倍", "🇺🇸美国04-0.1倍",
        "🇺🇸美国05-0.1倍", "🇺🇸美国06-0.1倍", "🇺🇸美国07-0.1倍",
        "🇺🇸美国圣何塞01 ", "🇺🇸美国圣何塞02 ", "🇺🇸美国圣何塞03 ",
        "🇺🇸美国圣何塞04 ", "🇺🇸美国圣何塞05 ", "🇺🇸美国圣何塞06 ", "🇺🇸美国圣何塞07 ",
        "🇺🇸美国洛杉矶08 ",
        "🇺🇸 美国1",
    ],
    minimum=2,
    group_label="ASXS余额故障转移",
)


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


openai_group_line = (
    "    - { name: OpenAI稳定, type: fallback, proxies: ["
    + ", ".join(yaml_quote(node) for node in openai_nodes)
    + "], url: 'https://chatgpt.com/backend-api/codex/responses', interval: 120, lazy: false }"
)
asxs_group_line = (
    "    - { name: ASXS余额故障转移, type: fallback, proxies: ["
    + ", ".join(yaml_quote(node) for node in asxs_nodes)
    + "], url: 'https://api.asxs.top/', interval: 300, lazy: false }"
)
rule_lines = [
    "    - 'DOMAIN,api.asxs.top,ASXS余额故障转移'",
    "    - 'DOMAIN-SUFFIX,chatgpt.com,OpenAI稳定'",
    "    - 'DOMAIN-SUFFIX,openai.com,OpenAI稳定'",
    "    - 'DOMAIN-SUFFIX,oaistatic.com,OpenAI稳定'",
    "    - 'DOMAIN,api.aabsv.sbs,DIRECT'",
    "    - 'DOMAIN-SUFFIX,oaiusercontent.com,OpenAI稳定'",
]

# --- 3. Compose: template with proxies section swapped in -------------------
# Pseudo info nodes that airports embed for clients (traffic/expiry notices).
PSEUDO_MARKERS = ("剩余流量", "套餐到期", "距离下次重置", "🪧", "官网", "Expire", "Traffic")
real_node_names = [
    name for name in available_names
    if not any(marker in name for marker in PSEUDO_MARKERS)
]
# Legacy "all nodes" groups that production rules still point at. Their
# membership is refreshed from the subscription on every run while the group
# name/type/health-check are preserved.
LEGACY_ALL_NODE_GROUPS = ("性价比机场", "自动选择", "故障转移")
legacy_proxies_value = "[" + ", ".join(yaml_quote(n) for n in real_node_names) + "]"

template_lines = template_path.read_text(encoding="utf-8").splitlines()
out = []
i = 0
inserted_group = inserted_rules = False
while i < len(template_lines):
    line = template_lines[i]
    if line.strip() == "proxies:":
        out.append(line)
        i += 1
        # Skip the template's own proxies block.
        while i < len(template_lines) and not re.match(r"^[a-zA-Z_-]+:", template_lines[i]):
            i += 1
        out.extend(proxy_lines)
        continue
    # Drop any previous copies of our groups/rules; they are re-injected below.
    if "OpenAI稳定" in line or "ASXS余额故障转移" in line:
        i += 1
        continue
    # Refresh legacy all-nodes groups with the new subscription's node list.
    if any(("name: %s," % group) in line or ("name: '%s'," % group) in line for group in LEGACY_ALL_NODE_GROUPS):
        line = re.sub(r"proxies: \[[^\]]*\]", "proxies: " + legacy_proxies_value, line)
    out.append(line)
    if line.strip() == "proxy-groups:" and not inserted_group:
        out.extend([openai_group_line, asxs_group_line])
        inserted_group = True
    if line.strip() == "rules:" and not inserted_rules:
        out.extend(rule_lines)
        inserted_rules = True
    i += 1

if not inserted_group or not inserted_rules:
    raise SystemExit("failed to inject CMSG routing groups into template config")

output_path.write_text("\n".join(out) + "\n", encoding="utf-8")
preferred_node_path.write_text(openai_nodes[0], encoding="utf-8")
print(f"proxies: {len(proxy_lines)} nodes from subscription", file=sys.stderr)
print(f"OpenAI稳定: {len(openai_nodes)} members, preferred={openai_nodes[0]}", file=sys.stderr)
print(f"ASXS余额故障转移: {len(asxs_nodes)} members", file=sys.stderr)
PYOPENAI

if ! validation_output=$(/usr/local/bin/mihomo -t -d /opt/mihomo -f "$tmp.config" 2>&1); then
  printf '%s\n' "$validation_output" >&2
  exit 1
fi
unset validation_output
if [[ "${CMSG_MIHOMO_DRY_RUN:-0}" == "1" ]]; then
  grep -n -E 'OpenAI稳定|ASXS余额故障转移' "$tmp.config"
  echo "mihomo config validation passed"
  exit 0
fi
install -m 0600 "$tmp" /opt/mihomo/subscription.yaml
install -m 0600 "$tmp.config" /opt/mihomo/config.yaml
docker compose -f /opt/mihomo/docker-compose.yml restart mihomo >/dev/null

# Select the measured preferred node after restart. Later changes are owned by
# CPA's route-recovery coordinator and must not be overridden by Mihomo.
controller_secret=$(cat "$controller_secret_file")
preferred_node=$(cat "$tmp.preferred")
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
