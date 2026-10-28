#!/usr/bin/env bash
# Builds Lumilo for release: signed with Developer ID, the disk image
# notarized, the update archive signed with the key from the Keychain.
# Extra arguments go to `mygo build`, e.g. -upload.
set -euo pipefail
cd "$(dirname "$0")/.."
MYGO_UPDATER_PRIVATE_KEY=$(security find-generic-password -a "$USER" -s "Lumilo update key" -w) ||
  { echo 'No "Lumilo update key" in the Keychain: run scripts/setup-release-signing.sh' >&2; exit 1; }
export MYGO_UPDATER_PRIVATE_KEY
exec go tool mygo build "$@"
