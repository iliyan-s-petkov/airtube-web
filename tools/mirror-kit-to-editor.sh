#!/bin/sh
# Mirror the design kit from the repo (the source of truth, per design-kit/
# README-repo.md) into the OpenDesign editor's project directory, so the UI tab
# shows exactly what is committed.
#
# Why this exists rather than a symlink: OpenDesign refuses to read a path that
# leaves its own project directory — "path escapes project dir via symlink" —
# which is a deliberate containment control and not something to route around.
# So the two directories stay separate and this makes them identical on demand.
#
# ONE DIRECTION ONLY, repo -> editor. The reverse is what SERVED.md warns about:
# an import from the editor silently reverts whatever was committed. This script
# has no reverse mode for that reason.
#
# Editor-only files are never touched: context/, preview/, image*.png,
# CLAUDE.md, SERVED.md, .file-versions/ and node-compile-cache/ are not in the
# repo and must survive.
set -eu

SRC="/Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0/design-kit"
DST="/Users/iliyan/Library/Application Support/Open Design/namespaces/release-stable/data/projects/2bd1e8df-2fb5-4be2-b61c-52d0eb1e3030"

[ -d "$SRC" ] || { echo "mirror: source missing: $SRC" >&2; exit 1; }
[ -d "$DST" ] || { echo "mirror: destination missing: $DST" >&2; exit 1; }

# Only the five served roots plus DESIGN.md. A sixth entry here would be a
# decision, not a convenience — the served allowlist lives in internal/designkit.
for n in ui_kits assets components.css tokens.css colors_and_type.css DESIGN.md examples; do
  [ -e "$SRC/$n" ] || continue
  if [ -d "$SRC/$n" ]; then
    # --delete so a file removed in the repo also disappears from the editor;
    # a stale copy is indistinguishable from a file someone meant to keep.
    rsync -a --delete "$SRC/$n/" "$DST/$n/"
  else
    rsync -a "$SRC/$n" "$DST/$n"
  fi
done

printf 'mirrored %s -> editor\n' "$(cd "$SRC" && git rev-parse --short HEAD 2>/dev/null || echo working-tree)"
