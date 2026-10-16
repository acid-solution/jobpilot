#!/usr/bin/env bash
# Prepare Ubuntu for CD. Does not start applications or change firewall rules.
# Run as root: bash prepare-cd-server.sh --check, then --apply.
set -euo pipefail

mode="${1:---check}"
case "$mode" in --check|--apply) ;; *) echo 'Use --check or --apply' >&2; exit 1 ;; esac
case "${DOCKER_APT_MIRROR:-official}" in
  official) docker_repository=https://download.docker.com/linux/ubuntu ;;
  aliyun) docker_repository=https://mirrors.aliyun.com/docker-ce/linux/ubuntu ;;
  *) echo 'DOCKER_APT_MIRROR must be official or aliyun.' >&2; exit 1 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo 'Run in the existing root terminal.' >&2; exit 1; }
deploy_user=acidsolution-deploy
stack_directory=/srv/jobpilot-stack
getent passwd "$deploy_user" >/dev/null || { echo 'Deployment account not found.' >&2; exit 1; }
[ ! -L "$stack_directory" ] || { echo 'Stack directory must not be a symlink.' >&2; exit 1; }
if [ -e "$stack_directory" ]; then
  [ -d "$stack_directory" ] && [ "$(stat -c %U "$stack_directory")" = "$deploy_user" ] || {
    echo 'Existing stack directory has unexpected type or owner; stopping without changing it.' >&2; exit 1;
  }
fi
. /etc/os-release
[ "$ID" = ubuntu ] || { echo 'This preparation script supports Ubuntu only.' >&2; exit 1; }
case "$VERSION_CODENAME" in jammy|noble|resolute) ;; *) echo 'Review Docker support for this Ubuntu version first.' >&2; exit 1 ;; esac
printf 'Plan: install Docker Engine/Compose if missing, enable Docker, create %s, and add %s to the docker group.\n' "$stack_directory" "$deploy_user"
printf '%s\n' 'Docker group membership grants broad control over this host. No unrestricted sudo rule is added.'
printf 'Docker package repository: %s\n' "$docker_repository"
if [ "$mode" = --check ]; then
  command -v docker || true
  id "$deploy_user"
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  # Do not automatically remove an existing alternative container runtime.
  for package in docker.io docker-compose podman-docker containerd runc; do
    if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then
      echo "Existing conflicting package $package; review manually before installation." >&2
      exit 1
    fi
  done
  apt-get update
  apt-get install -y ca-certificates curl
  install -m 0755 -d /etc/apt/keyrings
  key_file="$(mktemp)"
  trap 'rm -f -- "$key_file"' EXIT
  curl -fsSL --retry 3 --retry-all-errors --connect-timeout 10 --max-time 30 "$docker_repository/gpg" -o "$key_file"
  # Exact SHA-256 of the Docker official public key, checked on 2026-10-16.
  # Mirrors must deliver this same key; never disable APT signature verification.
  printf '1500c1f56fa9e26b9b8f42452a553675796ade0807cdce11975eb98170b3a570  %s\n' "$key_file" | sha256sum --check --status || {
    echo 'Docker signing key changed; verify it against Docker before updating this script.' >&2; exit 1;
  }
  source_file=/etc/apt/sources.list.d/acidsolution-docker.sources
  source_content="Types: deb
URIs: $docker_repository
Suites: $VERSION_CODENAME
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc"
  if [ -e "$source_file" ] && [ "$(cat "$source_file")" != "$source_content" ]; then
    echo 'Existing Docker repository file differs; preserve it and review manually.' >&2
    exit 1
  fi
  install -m 0644 "$key_file" /etc/apt/keyrings/docker.asc
  printf '%s\n' "$source_content" > "$source_file"
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
docker compose version >/dev/null || { echo 'Install a supported Docker Compose plugin first.' >&2; exit 1; }
systemctl enable docker
if ! systemctl is-active --quiet docker; then systemctl start docker; fi
getent group docker >/dev/null || groupadd docker
usermod -aG docker "$deploy_user"
if [ ! -d "$stack_directory" ]; then
  install -d -m 750 -o "$deploy_user" -g "$(id -gn "$deploy_user")" "$stack_directory"
fi
printf '%s\n' 'CD_SERVER_READY: reconnect SSH to pick up docker-group membership.'
docker --version
docker compose version
