#!/usr/bin/env bash
# Update one already-initialized application. Never applies a database downgrade.
# Usage: bash release.sh jobpilot|studyflow ghcr.io/acid-solution/APP@sha256:DIGEST
set -euo pipefail
umask 077

fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail 'Usage: release.sh jobpilot|studyflow image@sha256:digest'
application="$1"
image="$2"
case "$application" in
  jobpilot) service=api; image_key=JOBPILOT_IMAGE; port=18081 ;;
  studyflow) service=studyflow; image_key=STUDYFLOW_IMAGE; port=8081 ;;
  *) fail 'Unsupported application.' ;;
esac
[[ "$image" =~ ^ghcr\.io/acid-solution/${application}@sha256:[a-f0-9]{64}$ ]] || fail 'Expected an immutable image digest for this application.'
for required in docker flock awk gzip mktemp; do command -v "$required" >/dev/null || fail "Missing command: $required"; done

directory="$(cd -- "$(dirname -- "$0")" && pwd -P)"
images_file="$directory/.env.images"
state="$directory/.release-state"
for file in "$directory/.env.joint" "$images_file"; do
  [ -f "$file" ] && [ ! -L "$file" ] || fail "Missing or unsafe configuration file: $file"
done
[ ! -L "$state" ] || fail 'Release state directory must not be a symlink.'
mkdir -p "$state/backups"
chmod 700 "$state" "$state/backups"
exec 9>"$state/deployment.lock"
flock -w 600 9 || fail 'Another application deployment is still running.'

# All repositories use this same server lock, not just per-repository concurrency.
for key in JOBPILOT_IMAGE STUDYFLOW_IMAGE SHARED_AUTH_IMAGE; do
  value="$(awk -F= -v key="$key" '$1 == key {n++; value=substr($0,index($0,"=")+1)} END {if (n != 1) exit 1; print value}' "$images_file")" || fail "Expected exactly one $key."
  case "$key" in
    JOBPILOT_IMAGE) repository=jobpilot ;;
    STUDYFLOW_IMAGE) repository=studyflow ;;
    SHARED_AUTH_IMAGE) repository=jobpilot-auth ;;
  esac
  [[ "$value" =~ ^ghcr\.io/acid-solution/${repository}@sha256:[a-f0-9]{64}$ ]] || fail "Invalid pinned reference: $key"
done
previous="$(awk -F= -v key="$image_key" '$1 == key {print substr($0,index($0,"=")+1)}' "$images_file")"
unset JOBPILOT_IMAGE STUDYFLOW_IMAGE SHARED_AUTH_IMAGE
candidate="$(mktemp "$directory/.env.images.release.XXXXXX")"
trap 'rm -f -- "$candidate"' EXIT
awk -F= -v key="$image_key" -v image="$image" '$1 == key {print key "=" image; next} {print}' "$images_file" > "$candidate"
chmod 600 "$candidate"

compose_files=(-f "$directory/compose.yaml" -f "$directory/compose.joint.yaml")
if [ -f "$directory/.cd-http-test" ]; then
  compose_files+=(-f "$directory/compose.joint.http-test.yaml")
fi
compose_files+=(-f "$directory/compose.images.yaml")
active_images="$images_file"
compose() { docker compose --env-file "$directory/.env.joint" --env-file "$active_images" "${compose_files[@]}" "$@"; }
compose config --quiet
[ -n "$(compose ps -q "$service")" ] || fail 'Initialize and verify the joint stack before enabling automatic deployment.'
active_images="$candidate"
compose config --quiet
docker pull "$image"

release_id="$(date -u +%Y%m%dT%H%M%SZ)-$application-$$"
backup="$state/backups/$release_id.sql.gz"
case "$application" in
  jobpilot)
    compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip > "$backup"
    ;;
  studyflow)
    compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" mysqldump --single-transaction --no-tablespaces -u "$MYSQL_USER" "$MYSQL_DATABASE"' | gzip > "$backup"
    ;;
esac
[ -s "$backup" ] || fail 'Database backup did not produce a file; deployment stopped.'

if ! compose up -d --no-deps --no-build --wait --wait-timeout 180 "$service"; then
  compose logs --no-color --tail 100 "$service" > "$state/$release_id.failed.log" 2>&1 || true
  printf '%s FAILED %s %s previous=%s backup=%s\n' "$release_id" "$application" "$image" "$previous" "$backup" >> "$state/history.log"
  fail "Deployment failed. Private log: $state/$release_id.failed.log . Review database compatibility before deploying the previous image: $previous"
fi
if ! compose exec -T "$service" wget -q --spider "http://127.0.0.1:$port/health/ready" ||
   ! compose exec -T "$service" wget -q --spider "http://127.0.0.1:$port/"; then
  compose logs --no-color --tail 100 "$service" > "$state/$release_id.failed.log" 2>&1 || true
  printf '%s FAILED_SMOKE %s %s previous=%s backup=%s\n' "$release_id" "$application" "$image" "$previous" "$backup" >> "$state/history.log"
  fail "Post-deployment checks failed. Previous pinned configuration retained; inspect $state/$release_id.failed.log before recovery."
fi
mv -- "$candidate" "$images_file"
printf '%s SUCCESS %s %s previous=%s backup=%s\n' "$release_id" "$application" "$image" "$previous" "$backup" >> "$state/history.log"
printf 'DEPLOY_OK application=%s image=%s\n' "$application" "$image"
