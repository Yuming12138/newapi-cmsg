#!/usr/bin/env bash
set -euo pipefail

release=new-api-c585d7fafba9-registration-cmsg-20261013185056
staging=/tmp/new-api-c585d7fafba9-registration-cmsg-20261013185056
expected_sha=f85e23746da7e3795b2e87b5603f1378b7c2bb4bf6302ceb539a0779b506f205
previous_release=new-api-ce4d239f11b1-deleted-user-20261013123603
runtime=/opt/new-api
compose="$runtime/docker-compose.prod.yml"
backup="$runtime/backups/docker-compose.prod.yml.before-registration-cmsg-20261013185056"

test -f "$staging/new-api"
actual_sha=$(sha256sum "$staging/new-api" | awk '{print $1}')
test "$actual_sha" = "$expected_sha"

active_source=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/new-api"}}{{.Source}}{{end}}{{end}}' new-api)
test "$active_source" = "$runtime/releases/$previous_release/new-api"
test -f "$active_source"
test ! -e "$runtime/releases/$release"
test ! -e "$backup"
mkdir -p "$runtime/releases/$release" "$runtime/backups"
install -m 0755 "$staging/new-api" "$runtime/releases/$release/new-api"
installed_sha=$(sha256sum "$runtime/releases/$release/new-api" | awk '{print $1}')
test "$installed_sha" = "$expected_sha"
cp -p "$compose" "$backup"

python3 - "$compose" "$previous_release" "$release" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
before = path.read_bytes()
old = ("./releases/" + sys.argv[2] + "/new-api:/new-api:ro").encode()
new = ("./releases/" + sys.argv[3] + "/new-api:/new-api:ro").encode()
if before.count(old) != 1:
    raise SystemExit("Unexpected binary mount; refusing release switch")
path.write_bytes(before.replace(old, new, 1))
PY

rollback() {
    cp -p "$backup" "$compose"
    docker compose -f "$compose" up -d --no-deps --force-recreate new-api
}
if ! docker compose -f "$compose" config --quiet; then
    cp -p "$backup" "$compose"
    echo 'Compose validation failed; restored original config' >&2
    exit 1
fi
if ! docker compose -f "$compose" up -d --no-deps --force-recreate new-api; then
    rollback
    exit 1
fi
healthy=false
for attempt in $(seq 1 55); do
    state=$(docker inspect --format '{{.State.Health.Status}}' new-api)
    if [[ "$state" == healthy ]]; then
        healthy=true
        break
    fi
    sleep 1
done
if [[ "$healthy" != true ]]; then
    echo 'New API did not become healthy; rolling back' >&2
    rollback
    exit 1
fi

new_source=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/new-api"}}{{.Source}}{{end}}{{end}}' new-api)
test "$new_source" = "$runtime/releases/$release/new-api"
docker inspect --format 'status={{.State.Status}} health={{.State.Health.Status}} started={{.State.StartedAt}}' new-api
printf 'release=%s\nprevious_release=%s\nbackup=%s\nsha256=%s\n' "$release" "$previous_release" "$backup" "$installed_sha"
