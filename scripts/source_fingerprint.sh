#!/bin/sh
set -eu

{
  printf '%s\0' .dockerignore package.json package-lock.json apps/web/package.json apps/web/tsconfig.json apps/web/index.html packages/client/package.json scripts/source_fingerprint.sh
  find apps/web/src packages/client/src services/loom -type f -print0
} | LC_ALL=C sort -z -u | xargs -0 sha256sum | sha256sum | awk '{print $1}'
