// USB speaker: whatever raw audio the laptop sends over the USB cable comes out of the amp.
// Laptop side: 44.1 kHz, signed 16-bit little-endian, mono, written straight to /dev/ttyACM0.
#include <ESP_I2S.h>
#include <ESP_SR.h>   // only so uploads include the speech models (ESP SR partition scheme)

const int AMP_BCLK = 15, AMP_LRC = 16, AMP_DIN = 7;
const int RATE = 44100;

I2SClass amp;
uint8_t buf[2048];

void setup() {
  Serial.setRxBufferSize(32768);   // ~0.37 s of audio buffered against Wi-Fi-free USB hiccups
  Serial.begin(115200);
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  // mono sound on BOTH channels: the amp plays left+right averaged, so left-only would be half as loud
  amp.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH);
}

void loop() {
  int n = Serial.available() & ~1;   // whole samples only, or the audio turns to noise
  if (n > 0) {
    n = Serial.read(buf, min(n, (int)sizeof buf));
    amp.write(buf, n);   // blocks at playback speed, which paces the laptop through USB
  } else {
    memset(buf, 0, 512);   // nothing arriving: play silence so the amp never repeats old audio
    amp.write(buf, 512);
  }
}
