#!/bin/sh
# install-links.sh <build-dir> <dest-dir>
#
# Symlinks every binary in <build-dir> into <dest-dir>, which is expected to
# be on PATH. Why symlinks and not copies: the point is to test the
# development build, so a later `task cli:build` must take effect with no
# reinstall step. A copy would silently go stale, which is the exact bug
# this script exists to prevent.
#
# Invariant: an existing file in <dest-dir> that is NOT a symlink into
# <build-dir> is never touched. Overwriting a real installation (a release
# binary, a Homebrew shim) to make a dev build win would be unrecoverable
# for the user, so the script refuses and says what to do instead.
set -eu

build="$1"
dest="$2"

if [ ! -d "$build" ]; then
  echo "install: $build does not exist; run the build first" >&2
  exit 1
fi

mkdir -p "$dest"

linked=0
refused=0
for bin in "$build"/*; do
  [ -f "$bin" ] || continue
  [ -x "$bin" ] || continue
  name=$(basename "$bin")
  target="$dest/$name"
  if [ -L "$target" ]; then
    # Only a link that already points into this build directory is ours to
    # replace; one pointing elsewhere belongs to another checkout.
    current=$(readlink "$target")
    case "$current" in
      "$build"/*) ;;
      *)
        echo "install: $target is a symlink to $current; leaving it alone" >&2
        refused=$((refused + 1))
        continue
        ;;
    esac
  elif [ -e "$target" ]; then
    echo "install: $target exists and is not a symlink; leaving it alone" >&2
    echo "         remove it, or choose another directory: task install DEST=/some/dir" >&2
    refused=$((refused + 1))
    continue
  fi
  ln -sfn "$bin" "$target"
  echo "linked $target -> $bin"
  linked=$((linked + 1))
done

if [ "$linked" -eq 0 ]; then
  echo "install: no binaries found in $build" >&2
  exit 1
fi

# A link nobody can reach is not an install; say so rather than let the
# user discover it as "command not found".
case ":$PATH:" in
  *":$dest:"*) ;;
  *)
    echo "install: $dest is not on your PATH; add it to your shell profile:" >&2
    echo "         export PATH=\"$dest:\$PATH\"" >&2
    ;;
esac

if [ "$refused" -gt 0 ]; then
  exit 1
fi
