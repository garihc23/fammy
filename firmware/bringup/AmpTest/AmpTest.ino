// Amp + speaker test: plays a three-note chime every few seconds.
#include <ESP_I2S.h>
#include <ESP_SR.h>   // only so uploads include the speech models (needed with the ESP SR partition scheme)

// MAX98357 amp on its own I2S bus (the mic has the other one)
const int AMP_BCLK = 15, AMP_LRC = 16, AMP_DIN = 7;
const int RATE = 16000;
const int16_t VOLUME = 6000;   // out of 32767: the amp is loud, start gentle

I2SClass amp;
int16_t buf[256];

// write samples continuously: if the amp is starved of data it can repeat its last buffer as a buzz
void play(float hz, int ms) {
  static float phase = 0;
  float step = 2 * PI * hz / RATE;
  for (int left = RATE * ms / 1000; left > 0;) {
    int n = min(256, left);
    for (int i = 0; i < n; i++) {
      buf[i] = hz > 0 ? (int16_t)(sinf(phase) * VOLUME) : 0;
      phase += step;
      if (phase > 2 * PI) phase -= 2 * PI;
    }
    amp.write((uint8_t *)buf, n * 2);
    left -= n;
  }
}

void setup() {
  Serial.begin(115200);
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  if (!amp.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO)) {
    Serial.println("amp I2S FAILED");
  }
}

void loop() {
  Serial.println("chime");
  play(440, 250);   // A
  play(554, 250);   // C#
  play(659, 450);   // E
  play(0, 2000);    // silence
}
