#!/usr/bin/env bash
# Use the ESP as a USB speaker for this laptop: whatever Firefox plays comes out of Famyy's speaker.
# The board must be running firmware/bringup/UsbSpeaker:  ~/femi/firmware/build.sh bringup/UsbSpeaker upload
#   ~/femi/tools/esp-speaker.sh        start (Ctrl-C stops and puts the sound back on the laptop)
#   ~/femi/tools/esp-speaker.sh off    put the sound back on the laptop
set -uo pipefail
PORT=${PORT:-/dev/ttyACM0}
LAPTOP=$(pactl get-default-sink)
[[ "$LAPTOP" == famyy ]] && LAPTOP=$(pactl list short sinks | awk '$2!="famyy"{print $2; exit}')

firefox_streams() {
  pactl list sink-inputs | awk '/^Sink Input #/{id=substr($3,2)} /application.name = "Firefox"/{print id}'
}

off() {
  pkill -x parec 2>/dev/null; pkill -x sox 2>/dev/null
  for id in $(firefox_streams); do pactl move-sink-input "$id" "$LAPTOP" 2>/dev/null; done
  pactl set-default-sink "$LAPTOP"
  pactl list short modules | awk '/module-null-sink/ && /famyy/ {print $1}' | xargs -r -n1 pactl unload-module
  echo "sound is back on the laptop speakers"
}

if [[ "${1:-on}" == off ]]; then off; exit; fi

for _ in $(seq 20); do [[ -e "$PORT" ]] && break; sleep 0.5; done
if [[ ! -e "$PORT" ]]; then
  echo "The board isn't connected ($PORT missing): plug it into the socket marked USB and run this again."
  exit 1
fi

# a virtual output called "Famyy-speaker"; new sounds go there while this runs
pactl list short sinks | grep -q famyy || pactl load-module module-null-sink sink_name=famyy sink_properties=device.description=Famyy-speaker >/dev/null
pactl set-default-sink famyy
for id in $(firefox_streams); do
  pactl move-sink-input "$id" famyy
  pactl set-sink-input-volume "$id" 100%
done
trap off EXIT

stty -F "$PORT" raw -echo -hupcl
echo "Firefox now plays through Famyy's speaker ($PORT). Press Ctrl-C to stop."
# 44.1 kHz mono, +6 dB with a limiter (louder without crackling), straight down the USB cable
parec -d famyy.monitor --format=s16le --rate=44100 --channels=1 --latency-msec=40 \
  | sox -q -t raw -r 44100 -e signed-integer -b 16 -c 1 - -t raw - gain -l 6 > "$PORT"
