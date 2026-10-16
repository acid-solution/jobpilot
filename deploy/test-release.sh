#!/usr/bin/env bash
# Exercise release orchestration in a disposable container with a fake Docker CLI.
set -euo pipefail
[ -f /.dockerenv ] && [ -d /source ] || { echo 'Run only inside the disposable test container.' >&2; exit 1; }
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bin"
export DOCKER_TEST_LOG="$work/docker-calls.log"
cat > "$work/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$DOCKER_TEST_LOG"
if [ "$1" = --version ]; then echo 'Docker test stub'; exit 0; fi
if [ "$1" = pull ]; then [ "${FAIL_PULL:-0}" != 1 ]; exit; fi
[ "$1" = compose ]; shift
if [ "${1:-}" = version ]; then echo 'Docker Compose test stub'; exit 0; fi
while [ "$1" = --env-file ] || [ "$1" = -f ]; do shift 2; done
command="$1"; shift
case "$command" in
  config) exit 0 ;;
  ps) printf 'test-container\n' ;;
  up) sleep "${DOCKER_TEST_DELAY:-0}"; [ "${FAIL_UP:-0}" != 1 ] ;;
  logs) printf 'PRIVATE_DIAGNOSTIC_DO_NOT_FORWARD\n' ;;
  exec)
    [ "$1" = -T ]; shift
    case "$1" in
      postgres|mysql) [ "${FAIL_BACKUP:-0}" != 1 ]; printf 'disposable database backup\n' ;;
      api|studyflow) [ "${FAIL_SMOKE:-0}" != 1 ] ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
DOCKER
chmod +x "$work/bin/docker"
export PATH="$work/bin:$PATH"
sha_a="$(printf 'a%.0s' {1..64})"
sha_b="$(printf 'b%.0s' {1..64})"
sha_c="$(printf 'c%.0s' {1..64})"
sha_d="$(printf 'd%.0s' {1..64})"
sha_e="$(printf 'e%.0s' {1..64})"
fixture() {
  mkdir -p "$1"
  cp /source/release.sh "$1/"
  for name in compose.yaml compose.joint.yaml compose.images.yaml .env.joint; do : > "$1/$name"; done
  printf 'JOBPILOT_IMAGE=ghcr.io/acid-solution/jobpilot@sha256:%s\nSTUDYFLOW_IMAGE=ghcr.io/acid-solution/studyflow@sha256:%s\nSHARED_AUTH_IMAGE=ghcr.io/acid-solution/jobpilot-auth@sha256:%s\n' "$sha_a" "$sha_b" "$sha_c" > "$1/.env.images"
}
fixture "$work/success"
bash "$work/success/release.sh" jobpilot "ghcr.io/acid-solution/jobpilot@sha256:$sha_d" > "$work/success.log"
grep -q "JOBPILOT_IMAGE=ghcr.io/acid-solution/jobpilot@sha256:$sha_d" "$work/success/.env.images"
grep -q "STUDYFLOW_IMAGE=ghcr.io/acid-solution/studyflow@sha256:$sha_b" "$work/success/.env.images"
grep -q ' SUCCESS jobpilot ' "$work/success/.release-state/history.log"
test "$(stat -c %a "$work/success/.env.images")" = 600
gzip -dc "$work/success/.release-state/backups/"*.sql.gz | grep -q 'disposable database backup'
echo 'PASS: pinned single-service update, private backup and durable state'

fixture "$work/failure"
cp "$work/failure/.env.images" "$work/failure/snapshot"
if FAIL_UP=1 bash "$work/failure/release.sh" studyflow "ghcr.io/acid-solution/studyflow@sha256:$sha_e" > "$work/failure.log" 2>&1; then exit 1; fi
cmp "$work/failure/snapshot" "$work/failure/.env.images"
! grep -q PRIVATE_DIAGNOSTIC "$work/failure.log"
grep -q PRIVATE_DIAGNOSTIC "$work/failure/.release-state/"*.failed.log
grep -q ' FAILED studyflow ' "$work/failure/.release-state/history.log"
echo 'PASS: failed release keeps previous manifest and does not expose runtime logs'

fixture "$work/backup-failure"
if FAIL_BACKUP=1 bash "$work/backup-failure/release.sh" jobpilot "ghcr.io/acid-solution/jobpilot@sha256:$sha_d" > /dev/null 2>&1; then exit 1; fi
grep -q "JOBPILOT_IMAGE=ghcr.io/acid-solution/jobpilot@sha256:$sha_a" "$work/backup-failure/.env.images"
echo 'PASS: backup failure stops deployment'

fixture "$work/smoke-failure"
if FAIL_SMOKE=1 bash "$work/smoke-failure/release.sh" jobpilot "ghcr.io/acid-solution/jobpilot@sha256:$sha_d" > /dev/null 2>&1; then exit 1; fi
grep -q "JOBPILOT_IMAGE=ghcr.io/acid-solution/jobpilot@sha256:$sha_a" "$work/smoke-failure/.env.images"
grep -q FAILED_SMOKE "$work/smoke-failure/.release-state/history.log"
echo 'PASS: failed post-deployment check does not commit new release state'

fixture "$work/concurrent"
DOCKER_TEST_DELAY=0.2 bash "$work/concurrent/release.sh" jobpilot "ghcr.io/acid-solution/jobpilot@sha256:$sha_d" > /dev/null &
first=$!
DOCKER_TEST_DELAY=0.2 bash "$work/concurrent/release.sh" studyflow "ghcr.io/acid-solution/studyflow@sha256:$sha_e" > /dev/null &
second=$!
wait "$first"
wait "$second"
grep -q "JOBPILOT_IMAGE=ghcr.io/acid-solution/jobpilot@sha256:$sha_d" "$work/concurrent/.env.images"
grep -q "STUDYFLOW_IMAGE=ghcr.io/acid-solution/studyflow@sha256:$sha_e" "$work/concurrent/.env.images"
test "$(grep -c ' SUCCESS ' "$work/concurrent/.release-state/history.log")" = 2
echo 'PASS: cross-repository server lock prevents lost updates'

if bash "$work/concurrent/release.sh" jobpilot 'ghcr.io/another-owner/jobpilot:latest' > /dev/null 2>&1; then exit 1; fi
echo 'PASS: mutable and unexpected image references are rejected'

bash -n /source/prepare-cd-server.sh
# Only test the existing-Docker branch; real package installation needs the server.
printf 'ID=ubuntu\nVERSION_CODENAME=resolute\n' > /etc/os-release
getent passwd acidsolution-deploy >/dev/null || useradd -m -U -s /bin/bash -p '*' acidsolution-deploy
cat > "$work/bin/systemctl" <<'SYSTEMCTL'
#!/bin/sh
[ "${1:-}" != restart ] || exit 1
exit 0
SYSTEMCTL
chmod +x "$work/bin/systemctl"
bash /source/prepare-cd-server.sh --check > /dev/null
test ! -e /srv/jobpilot-stack
bash /source/prepare-cd-server.sh --apply > /dev/null
test "$(stat -c %U /srv/jobpilot-stack)" = acidsolution-deploy
id -nG acidsolution-deploy | grep -qw docker
echo 'PASS: server dry run does not create state; existing Docker is not restarted'
