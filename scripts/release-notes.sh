#!/usr/bin/env bash
# Puts an install line at the top of the GitHub release's notes: the disk
# image to download, or Homebrew. The release also holds the update
# archive, the deltas and the manifest, which are for Lumilo's own updates,
# not for people. The notes in the update manifest stay the changelog's.
# Running it again changes nothing.
set -euo pipefail
version=${1:?usage: scripts/release-notes.sh <version>}
repo=kotuke/lumilo
# GitHub names an asset with its spaces as dots.
dmg="Lumilo.$version.arm64.dmg"
url="https://github.com/$repo/releases/download/v$version/$dmg"
line="**Install:** download [\`$dmg\`]($url) and drag Lumilo to Applications, or run \`brew install --cask kotuke/tap/lumilo\`. The other files are for Lumilo's built-in updates."
body=$(gh release view "v$version" --repo "$repo" --json body -q .body)
case "$body" in
"**Install:**"*) exit 0 ;;
esac
printf '%s\n\n%s\n' "$line" "$body" | gh release edit "v$version" --repo "$repo" --notes-file -
