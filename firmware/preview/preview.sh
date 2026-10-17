#!/usr/bin/env bash
# Render a screen animation from the firmware's own drawing code into a GIF (see main.cpp).
#   ./preview.sh talk|fo|shrug|icons|faces [out.gif]
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
gfx="$HOME/Arduino/libraries/Adafruit_GFX_Library"
what="${1:-fo}"
out="${2:-$here/out/$what.gif}"
build="$here/out/build"
frames="$here/out/frames-$what"
mkdir -p "$build" "$(dirname "$out")"
rm -rf "$frames" && mkdir -p "$frames"

g++ -std=c++17 -O2 -w -DARDUINO=100 -I"$here/stub" -I"$gfx" \
  "$here/main.cpp" "$here/../femi/creature.cpp" "$here/../femi/fo.cpp" "$here/../femi/bubble.cpp" "$here/../femi/words.cpp" "$gfx/Adafruit_GFX.cpp" \
  -o "$build/preview"
"$build/preview" "$what" "$frames"
ffmpeg -loglevel error -y -framerate 20 -i "$frames/f%04d.ppm" \
  -vf "scale=640:480:flags=neighbor,split[a][b];[a]palettegen=max_colors=64[p];[b][p]paletteuse=dither=none" \
  "$out"
echo "$out ($(ls "$frames" | wc -l) frames, $(du -h "$out" | cut -f1))"
