#!/bin/sh
set -eu

# Docker Desktop exposes Compose as `docker compose`; OrbStack may instead
# install the compatible standalone `docker-compose` command.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  exec docker compose "$@"
fi

if command -v docker-compose >/dev/null 2>&1; then
  exec docker-compose "$@"
fi

echo "Docker Compose is required: install the Docker Compose plugin or docker-compose." >&2
exit 127
