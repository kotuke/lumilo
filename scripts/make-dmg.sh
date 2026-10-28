#!/usr/bin/env bash
# Packs Lumilo.app into a disk image whose window shows the app, a link to
# Applications and packaging/background.tiff, with an arrow between them.
# MyGo's own image has no background; this one replaces it.
#
#   scripts/make-dmg.sh build/darwin-arm64/Lumilo.app "build/darwin-arm64/Lumilo 0.1.0 arm64.dmg"
#
# Finder lays the window out, so the first run asks to let the terminal
# control Finder. The sizes match packaging/dmg.py.
set -euo pipefail
app=$1 out=$2
name=$(basename "$app" .app)
work=$(mktemp -d)
mnt=
trap '[ -n "$mnt" ] && hdiutil detach -quiet -force "$mnt" 2>/dev/null; rm -rf "$work"' EXIT

mkdir "$work/src" "$work/src/.background"
ditto --noclone "$app" "$work/src/$name.app"
ln -s /Applications "$work/src/Applications"
cp "$(dirname "$0")/../packaging/background.tiff" "$work/src/.background/background.tiff"

hdiutil create -quiet -ov -volname "$name" -srcfolder "$work/src" -fs HFS+ \
  -fsargs "-c c=64,a=16,e=16" -format UDRW -size 100m "$work/rw.dmg"
# Finder sees the image only where it mounts it, under /Volumes.
mnt=$(hdiutil attach -readwrite -noverify -noautoopen "$work/rw.dmg" | awk -F'\t' '/\/Volumes\// {print $NF}')

# The window: 640 x 400 points, icons 128 points at (170, 205) and (470, 205).
osascript <<OSA
tell application "Finder"
  set d to folder (POSIX file "$mnt" as alias)
  open d
  set w to container window of d
  set current view of w to icon view
  set toolbar visible of w to false
  set statusbar visible of w to false
  set bounds of w to {200, 120, 840, 548}
  set o to icon view options of w
  set arrangement of o to not arranged
  set icon size of o to 128
  set text size of o to 13
  set background picture of o to file ".background:background.tiff" of d
  set position of item "$name.app" of d to {170, 205}
  set position of item "Applications" of d to {470, 205}
  update d without registering applications
  delay 1
  close w
end tell
OSA
chflags hidden "$mnt/.background"
# After Finder, which drops the volume icon and its flag while it lays out.
cp "$app/Contents/Resources/AppIcon.icns" "$mnt/.VolumeIcon.icns"
SetFile -a C "$mnt"
rm -rf "$mnt/.fseventsd" "$mnt/.Trashes"
sync
hdiutil detach -quiet "$mnt"
mnt=
rm -f "$out"
hdiutil convert -quiet "$work/rw.dmg" -format ULMO -o "$out"
echo "$out"
