#!/usr/bin/env bash
set -euo pipefail

# Switch cmsg-root New API back to an existing release binary.

host="${CMSG_HOST:-cmsg-root}"
remote_dir="${CMSG_REMOTE_DIR:-/opt/new-api}"
compose_file="${CMSG_COMPOSE_FILE:-docker-compose.prod.yml}"
release="${1:-}"

if [[ -z "$release" ]]; then
  echo "usage: $0 <release-name>" >&2
  echo "available releases:" >&2
  ssh "$host" "cd '$remote_dir' && find releases -maxdepth 1 -mindepth 1 -type d -name 'new-api-*' -printf '%TY-%Tm-%Td %TH:%TM %f\n' | sort -r | head -n 20" >&2
  exit 2
fi

case "$release" in
  new-api-*) ;;
  *)
    echo "error: release name must start with new-api-" >&2
    exit 1
    ;;
esac

ssh "$host" "sudo -n bash -s -- '$remote_dir' '$compose_file' '$release'" <<'REMOTE'
set -euo pipefail
remote_dir="$1"
compose_file="$2"
release="$3"

cd "$remote_dir"
target="releases/$release/new-api"
if [[ ! -f "$target" ]]; then
  echo "missing release binary: $remote_dir/$target" >&2
  exit 1
fi
if ! grep -qE '\./releases/[^:]+/new-api:/new-api:ro' "$compose_file"; then
  echo "compose does not mount a release binary to /new-api" >&2
  exit 1
fi

stamp="$(date +%Y%m%d%H%M%S)"
cp "$compose_file" "$compose_file.bak.rollback-$stamp"
perl -0pi -e "s#\./releases/[^:]+/new-api:/new-api:ro#./releases/$release/new-api:/new-api:ro#" "$compose_file"
grep -n './releases/.*/new-api:/new-api:ro' "$compose_file"

docker compose -f "$compose_file" up -d new-api
for _ in $(seq 1 45); do
  if docker compose -f "$compose_file" ps new-api | grep -q healthy; then
    docker compose -f "$compose_file" ps new-api
    exit 0
  fi
  sleep 1
done
docker compose -f "$compose_file" ps new-api
docker logs --tail 120 new-api
exit 1
REMOTE

echo "rolled back to $release"
