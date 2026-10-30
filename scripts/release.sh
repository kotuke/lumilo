#!/usr/bin/env bash
# Builds Lumilo for release: signed with Developer ID, the disk image
# notarized, the update archive signed with the key from the Keychain.
# Extra arguments go to `mygo build`, e.g. -upload.
#
# MyGo's disk image has no background, so scripts/make-dmg.sh packs the
# notarized app again into one with an arrow to Applications, which is
# signed, notarized and, with -upload, put in the release in its place;
# scripts/release-notes.sh then says in the release's notes which file to
# install.
set -euo pipefail
cd "$(dirname "$0")/.."
MYGO_UPDATER_PRIVATE_KEY=$(security find-generic-password -a "$USER" -s "Lumilo update key" -w) ||
  { echo 'No "Lumilo update key" in the Keychain: run scripts/setup-release-signing.sh' >&2; exit 1; }
export MYGO_UPDATER_PRIVATE_KEY
go tool mygo build "$@"

version=$(plutil -extract version raw -o - mygo.json)
dmg="build/darwin-arm64/Lumilo $version arm64.dmg"
[ -f "$dmg" ] || exit 0
identity=$(plutil -extract macos.signingIdentity raw -o - mygo.json)
profile=$(plutil -extract macos.notarize.keychainProfile raw -o - mygo.json)
scripts/make-dmg.sh build/darwin-arm64/Lumilo.app "$dmg"
codesign --force --timestamp --sign "$identity" "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$profile" --wait
xcrun stapler staple "$dmg"
spctl --assess --type install "$dmg"
for a in "$@"; do
  if [ "$a" = -upload ]; then
    gh release upload "v$version" "$dmg" --repo kotuke/lumilo --clobber
    scripts/release-notes.sh "$version"
  fi
done
