#!/bin/sh
# One-way re-import of the design kit from the OpenDesign editor's project
# directory into design-kit/. See design-kit/README-repo.md.
set -eu

src="${1:-$HOME/Library/Application Support/Open Design/namespaces/release-stable/data/projects/2bd1e8df-2fb5-4be2-b61c-52d0eb1e3030}"
dst="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/design-kit"

# design-kit/ is the source of truth and is edited directly, so this import runs
# the wrong way: --delete would revert anything committed here but not present
# in the editor's copy, silently and completely. Gated rather than removed
# because a deliberate re-import is still occasionally right.
[ "${SYNC_FROM_EDITOR:-}" = "1" ] || {
	cat >&2 <<-EOF
		refusing to run: design-kit/ in this repo is the source of truth.

		This copies the editor's directory OVER it, with --delete, so work
		committed here and absent there would be lost.

		For a deliberate re-import: SYNC_FROM_EDITOR=1 $0
		Then check \`git status --short design-kit\` before committing.
	EOF
	exit 1
}

[ -f "$src/ui_kits/app/index.html" ] || {
	echo "not a design kit: $src" >&2
	exit 1
}

# --delete so a file the designer removed disappears here too; without it the
# kit only ever grows and the served tree stops matching what the editor shows.
# The excludes are what the editor generates rather than what anyone wrote.
rsync -a --delete \
	--exclude '.git/' \
	--exclude '.file-versions/' \
	--exclude 'node-compile-cache/' \
	--exclude 'context/' \
	--exclude 'preview/' \
	--exclude '*.artifact.json' \
	--exclude 'screenshot-*.png' \
	--exclude 'image*.png' \
	--exclude 'CLAUDE.md' \
	--exclude '.gitignore' \
	--exclude 'SERVED.md' \
	--exclude 'README-repo.md' \
	-- "$src/" "$dst/"

echo "synced to $dst; review with: git status --short design-kit"
