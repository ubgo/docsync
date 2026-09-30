#!/bin/sh
# uninstall-links.sh <build-dir> <dest-dir>
#
# Removes the symlinks install-links.sh created. Invariant: only symlinks
# that resolve into <build-dir> are removed, so a real installation or
# another checkout's link is never deleted by an uninstall here.
set -eu

build="$1"
dest="$2"

removed=0
for bin in "$build"/*; do
  [ -f "$bin" ] || continue
  name=$(basename "$bin")
  target="$dest/$name"
  [ -L "$target" ] || continue
  case "$(readlink "$target")" in
    "$build"/*)
      rm -f "$target"
      echo "removed $target"
      removed=$((removed + 1))
      ;;
  esac
done

if [ "$removed" -eq 0 ]; then
  echo "uninstall: nothing linked from $build in $dest"
fi
