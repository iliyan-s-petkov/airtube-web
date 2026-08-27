#!/usr/bin/env bash
# Build the airbg.org basemap on this host, following docs/tiles.md exactly.
# Pins: planetiler 0.8.3, font-maker v0.0.1 (46fac6c), NotoSans-v2.015.
set -euo pipefail

DATE="${DATE:-20260827}"
WORK=/opt/basemap
TILES=/var/lib/airbg/tiles
ARCHIVE="bulgaria-${DATE}.pmtiles"

log() { echo "[$(date -u +%H:%M:%S)] $*"; }

log "packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# clang is not a preference: font-maker's CMakeLists.txt hardcodes
# /usr/bin/clang and /usr/bin/clang++, and cmake fails at configure without it.
# font-maker's CMakeLists.txt hardcodes /usr/bin/clang++ and does
# find_package on Boost 1.73 (headers only) and Freetype. All three are
# configure-time failures, not link-time ones, so they are not optional.
apt-get install -y -qq openjdk-21-jre-headless build-essential clang cmake git \
    curl unzip pkg-config libboost-dev libfreetype-dev

mkdir -p "$WORK" "$TILES"
cd "$WORK"

log "extract"
[ -f bulgaria-latest.osm.pbf ] || \
  curl -fsSL -o bulgaria-latest.osm.pbf https://download.geofabrik.de/europe/bulgaria-latest.osm.pbf

log "planetiler 0.8.3"
[ -f planetiler.jar ] || \
  curl -fsSL -o planetiler.jar https://github.com/onthegomap/planetiler/releases/download/v0.8.3/planetiler.jar

log "archive -> $ARCHIVE"
if [ ! -f "$TILES/$ARCHIVE" ]; then
  java -Xmx5g -jar planetiler.jar \
    --osm-path=bulgaria-latest.osm.pbf \
    --output="$WORK/$ARCHIVE" \
    --download \
    --force
  mv "$WORK/$ARCHIVE" "$TILES/$ARCHIVE"
fi

log "font-maker v0.0.1"
if [ ! -x "$WORK/font-maker/font-maker" ]; then
  rm -rf font-maker
  git clone --recursive https://github.com/maplibre/font-maker.git
  cd font-maker
  git checkout v0.0.1
  git submodule update --init --recursive
  # Two toolchain drifts since this tag, both fixed with flags rather than by
  # unpinning — the pin is what keeps glyph output reproducible.
  #
  # -include cstdint: glyph_foundry.hpp spells uint32_t without including
  # <cstdint>. It compiled when libstdc++ still pulled that header in
  # transitively; since libstdc++ 13 it does not, and the failure is
  # "unknown type name 'uint32_t'" in a vendored header.
  #
  # -Wno-c++11-narrowing*: narrowing in glyph metric arithmetic that the
  # upstream tool has always performed, promoted to an error by newer clang.
  cmake . -DCMAKE_CXX_FLAGS="-include cstdint -Wno-c++11-narrowing -Wno-c++11-narrowing-const-reference -Wno-error"
  make -j"$(nproc)"
  cd "$WORK"
fi

log "Noto Sans NotoSans-v2.015"
if [ ! -d "$WORK/noto" ]; then
  curl -fsSL -o noto.zip \
    https://github.com/notofonts/latin-greek-cyrillic/releases/download/NotoSans-v2.015/NotoSans-v2.015.zip
  mkdir -p noto && unzip -qo noto.zip -d noto
fi
REG=$(find "$WORK/noto" -name 'NotoSans-Regular.ttf' | head -1)
MED=$(find "$WORK/noto" -name 'NotoSans-Medium.ttf' | head -1)
[ -n "$REG" ] && [ -n "$MED" ] || { echo "missing Noto ttf files"; exit 1; }

log "glyphs"
if [ ! -d "$TILES/glyphs/Noto Sans Regular" ]; then
  cd "$WORK"
  rm -rf glyph-out-regular glyph-out-medium
  ./font-maker/font-maker glyph-out-regular "$REG"
  ./font-maker/font-maker glyph-out-medium "$MED"
  mkdir -p "$TILES/glyphs"
  mv "glyph-out-regular/Noto Sans Regular" "$TILES/glyphs/"
  mv "glyph-out-medium/Noto Sans Medium" "$TILES/glyphs/"
  rm -rf glyph-out-regular glyph-out-medium
fi

log "done"
ls -la "$TILES"
ls "$TILES/glyphs"
