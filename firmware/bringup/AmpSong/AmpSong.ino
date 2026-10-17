// Speaker test at full volume: plays "Ode to Joy" (Beethoven, public domain) with a warm, organ-like tone.
#include <ESP_I2S.h>
#include <ESP_SR.h>   // only so uploads include the speech models (ESP SR partition scheme)

const int AMP_BCLK = 15, AMP_LRC = 16, AMP_DIN = 7;
const int RATE = 16000;
const float VOLUME = 1.0f;     // 1.0 = full scale. Lower it if the speaker rattles.
const int BEAT_MS = 450;       // one quarter note

// note frequencies (Hz)
const float C4 = 261.63, D4 = 293.66, E4 = 329.63, F4 = 349.23, G4 = 392.00, G3 = 196.00;
struct Note { float hz; float beats; };
const Note SONG[] = {
  {E4,1},{E4,1},{F4,1},{G4,1}, {G4,1},{F4,1},{E4,1},{D4,1}, {C4,1},{C4,1},{D4,1},{E4,1}, {E4,1.5},{D4,0.5},{D4,2},
  {E4,1},{E4,1},{F4,1},{G4,1}, {G4,1},{F4,1},{E4,1},{D4,1}, {C4,1},{C4,1},{D4,1},{E4,1}, {D4,1.5},{C4,0.5},{C4,2},
  {D4,1},{D4,1},{E4,1},{C4,1}, {D4,1},{E4,0.5},{F4,0.5},{E4,1},{C4,1}, {D4,1},{E4,0.5},{F4,0.5},{E4,1},{D4,1}, {C4,1},{D4,1},{G3,2},
  {E4,1},{E4,1},{F4,1},{G4,1}, {G4,1},{F4,1},{E4,1},{D4,1}, {C4,1},{C4,1},{D4,1},{E4,1}, {D4,1.5},{C4,0.5},{C4,2},
};

I2SClass amp;
int16_t buf[256];

void playNote(float hz, int ms) {
  int total = RATE * ms / 1000, attack = RATE / 100, release = RATE / 12;   // 10 ms in, 80 ms out: no clicks
  float phase = 0, step = 2 * PI * hz / RATE;
  for (int n = 0; n < total;) {
    int count = min(256, total - n);
    for (int i = 0; i < count; i++, n++) {
      float env = n < attack ? (float)n / attack : (total - n < release ? (float)(total - n) / release : 1.0f);
      // fundamental plus two softer harmonics: rounder than a plain beep
      float s = sinf(phase) + 0.35f * sinf(2 * phase) + 0.15f * sinf(3 * phase);
      buf[i] = (int16_t)(s / 1.5f * 32000 * VOLUME * env);
      phase += step;
      if (phase > 2 * PI) phase -= 2 * PI;
    }
    amp.write((uint8_t *)buf, count * 2);
  }
}

void setup() {
  Serial.begin(115200);
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  // mono sound on BOTH channels: the amp plays left+right averaged, so left-only would be half as loud
  amp.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH);
}

void loop() {
  Serial.println("playing Ode to Joy");
  for (const Note &n : SONG) playNote(n.hz, (int)(n.beats * BEAT_MS));
  memset(buf, 0, sizeof buf);
  for (int i = 0; i < RATE * 2 / 256; i++) amp.write((uint8_t *)buf, sizeof buf);   // 2 s of silence, then again
}
