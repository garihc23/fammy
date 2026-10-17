// Amp diagnostic: reports whether the amp's I2S bus starts and how long each write takes.
#include <ESP_I2S.h>
#include <ESP_SR.h>   // only so uploads include the speech models (ESP SR partition scheme)

const int AMP_BCLK = 15, AMP_LRC = 16, AMP_DIN = 7;
I2SClass amp;
bool ok = false;
int16_t buf[256];

void setup() {
  Serial.begin(115200);
  delay(4000);   // time for a monitor to attach after reset
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  ok = amp.begin(I2S_MODE_STD, 16000, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO);
  amp.setTimeout(200);
}

void loop() {
  static float phase = 0;
  uint32_t t0 = millis();
  size_t total = 0;
  for (int k = 0; k < 32; k++) {   // ~0.5 s of a 440 Hz tone
    for (int i = 0; i < 256; i++) {
      buf[i] = (int16_t)(sinf(phase) * 6000);
      phase += 2 * PI * 440 / 16000;
      if (phase > 2 * PI) phase -= 2 * PI;
    }
    total += amp.write((uint8_t *)buf, sizeof buf);
  }
  Serial.printf("begin=%d  wrote %u of %u bytes in %lu ms (expect ~512 ms)  lastError=%d\n",
                ok, (unsigned)total, (unsigned)(32 * sizeof buf), millis() - t0, amp.lastError());
  delay(500);
}
