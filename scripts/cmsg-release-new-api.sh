#!/usr/bin/env bash
set -euo pipefail

# Build and deploy the CMSG New API binary from the WSL source checkout.
# This script intentionally does not build on cmsg-root.

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

host="${CMSG_HOST:-cmsg-root}"
remote_dir="${CMSG_REMOTE_DIR:-/opt/new-api}"
compose_file="${CMSG_COMPOSE_FILE:-docker-compose.prod.yml}"
public_status_url="${CMSG_PUBLIC_STATUS_URL:-https://api.cmsg666.xyz/api/status}"
keep_prior="${CMSG_KEEP_PRIOR_RELEASES:-3}"
skip_tests="${CMSG_SKIP_TESTS:-0}"

branch="$(git branch --show-current)"
if [[ "${CMSG_ALLOW_NON_DEV_BRANCH:-0}" != "1" && "$branch" != "dev/cmsg" ]]; then
  echo "error: expected dev/cmsg, got $branch" >&2
  exit 1
fi

git fetch origin dev/cmsg
if [[ "${CMSG_ALLOW_OUT_OF_DATE:-0}" != "1" ]]; then
  local_head="$(git rev-parse HEAD)"
  remote_head="$(git rev-parse origin/dev/cmsg)"
  if [[ "$local_head" != "$remote_head" ]]; then
    echo "error: HEAD is not origin/dev/cmsg" >&2
    echo "local : $local_head" >&2
    echo "remote: $remote_head" >&2
    exit 1
  fi
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "error: working tree has uncommitted changes" >&2
  git status --short
  exit 1
fi

short_sha="$(git rev-parse --short HEAD)"
release="${CMSG_RELEASE_NAME:-new-api-${short_sha}-$(date +%Y%m%d%H%M%S)}"
build_root="${CMSG_BUILD_CACHE:-$HOME/.cache/new-api-cmsg-releases}"
out_dir="$build_root/$release"
out_bin="$out_dir/new-api"

rm -rf "$out_dir"
mkdir -p "$out_dir"

echo "release=$release"
echo "commit=$short_sha"

git diff --check

if [[ "$skip_tests" != "1" ]]; then
  (
    cd web/default
    bun run typecheck
  )
  go test ./controller ./model ./service
fi

(
  cd web/default
  DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$short_sha" bun run build
)

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=${short_sha}' -extldflags '-static'" \
  -o "$out_bin" .

file "$out_bin"
if ! file "$out_bin" | grep -q 'statically linked'; then
  echo "error: built binary is not statically linked" >&2
  exit 1
fi

local_sha="$(sha256sum "$out_bin" | awk '{print $1}')"
echo "$local_sha  $out_bin" | tee "$out_dir/SHA256SUMS"

remote_tmp="/tmp/$release/new-api"
ssh "$host" "mkdir -p '/tmp/$release'"
rsync -avP "$out_bin" "$host:$remote_tmp"

remote_tmp_sha="$(ssh "$host" "sha256sum '$remote_tmp'" | awk '{print $1}')"
if [[ "$remote_tmp_sha" != "$local_sha" ]]; then
  echo "error: uploaded artifact checksum mismatch" >&2
  exit 1
fi

ssh "$host" "sudo -n bash -s -- '$remote_dir' '$compose_file' '$release' '$remote_tmp' '$local_sha' '$keep_prior'" <<'REMOTE'
set -euo pipefail
remote_dir="$1"
compose_file="$2"
release="$3"
remote_tmp="$4"
expected_sha="$5"
keep_prior="$6"

cd "$remote_dir"
if [[ ! -f "$compose_file" ]]; then
  echo "missing compose file: $remote_dir/$compose_file" >&2
  exit 1
fi
if ! grep -qE '\./releases/[^:]+/new-api:/new-api:ro' "$compose_file"; then
  echo "compose does not mount a release binary to /new-api" >&2
  exit 1
fi

mkdir -p "releases/$release"
install -m 0755 "$remote_tmp" "releases/$release/new-api"
actual_sha="$(sha256sum "releases/$release/new-api" | awk '{print $1}')"
if [[ "$actual_sha" != "$expected_sha" ]]; then
  echo "installed artifact checksum mismatch" >&2
  exit 1
fi

cp "$compose_file" "$compose_file.bak.$release"
perl -0pi -e "s#\./releases/[^:]+/new-api:/new-api:ro#./releases/$release/new-api:/new-api:ro#" "$compose_file"
grep -n './releases/.*/new-api:/new-api:ro' "$compose_file"

docker compose -f "$compose_file" up -d new-api

for _ in $(seq 1 45); do
  if docker compose -f "$compose_file" ps new-api | grep -q healthy; then
    docker compose -f "$compose_file" ps new-api
    break
  fi
  sleep 1
done
if ! docker compose -f "$compose_file" ps new-api | grep -q healthy; then
  docker compose -f "$compose_file" ps new-api
  docker logs --tail 120 new-api
  exit 1
fi

current_release="$release"
mapfile -t old_releases < <(
  find releases -maxdepth 1 -mindepth 1 -type d -name 'new-api-*' -printf '%T@ %f\n' |
    sort -rn |
    awk -v current="$current_release" -v keep="$keep_prior" '
      $2 == current { next }
      kept < keep { kept++; next }
      { print $2 }
    '
)
for old in "${old_releases[@]}"; do
  case "$old" in
    new-api-*) rm -rf -- "releases/$old" ;;
  esac
done
REMOTE

ssh "$host" "curl -fsS --max-time 10 http://127.0.0.1:3000/api/status | head -c 400; echo"
curl -fsS --max-time 15 "$public_status_url" | head -c 400
echo
echo "deployed $release"
