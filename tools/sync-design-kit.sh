#!/bin/sh
# Copy the design kit out of the OpenDesign editor's project directory into
# design-kit/. Run it after a design session; `git status` then shows what
# changed. See design-kit/README-repo.md.
set -eu

src="${1:-$HOME/Library/Application Support/Open Design/namespaces/release-stable/data/projects/2bd1e8df-2fb5-4be2-b61c-52d0eb1e3030}"
dst="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/design-kit"

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
	-- "$src/" "$dst/"

echo "synced to $dst; review with: git status --short design-kit"
