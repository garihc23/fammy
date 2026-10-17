#!/usr/bin/env bash
# Compile (and optionally upload) a sketch with Femi's board settings, without opening the IDE.
#   ./build.sh femi              compile firmware/femi
#   ./build.sh femi upload       compile and upload to /dev/ttyACM0
#   ./build.sh bringup/AmpTest   compile a bring-up test
set -euo pipefail
cd "$(dirname "$0")"
SKETCH="${1:?sketch folder, e.g. femi}"
FQBN="esp32:esp32:esp32s3:PSRAM=opi,FlashSize=16M,PartitionScheme=esp_sr_16,CDCOnBoot=cdc,FlashMode=qio,USBMode=hwcdc,LoopCore=0,EventsCore=1"
BUILD="${TMPDIR:-/tmp}/femi-build/$(basename "$SKETCH")"
arduino-cli compile --config-file ~/.arduinoIDE/arduino-cli.yaml --fqbn "$FQBN" --build-path "$BUILD" "$SKETCH"
if [[ "${2:-}" == "upload" ]]; then
  arduino-cli upload --config-file ~/.arduinoIDE/arduino-cli.yaml --fqbn "$FQBN" --input-dir "$BUILD" -p "${PORT:-/dev/ttyACM0}" "$SKETCH"
fi
