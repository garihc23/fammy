#include <ESP_I2S.h>
#include "voice.h"
#include "net.h"
#include "femi.h"
#include "shared.h"
#include "config.h"

// ================= recording =================

static Ring mic;
static volatile bool recording = false;
static volatile int level = 0;

// voice activity detection on 30 ms frames: the request ends after a short silence
static const int FRAME = 480;                 // samples per frame at 16 kHz
static const int SPEECH_MIN = 900;            // the quietest loudness that can count as speech (the mic has +12 dB gain)
static const int SPEECH_RUN = 3;              // 90 ms of voice in a row: a lone click or fan rattle isn't speech
static const int END_SILENT_FRAMES = 20;      // ~0.6 s of quiet after speaking ends the request
static const int MAX_FRAMES = 400;            // 12 s at most
static const int GIVE_UP_FRAMES = 170;        // 5 s without speech: probably a false wake, drop it
static const int CHIME_SKIP = 16000 * 3 / 10;   // the first 0.3 s holds Famyy's own "listening" chime: never send or judge it
static int64_t sumSq;
static int skip, frameN, frames, speechFrames, speechRun, silentFrames;
static float noise;
static bool heard;

Ring &micRing() { return mic; }
bool voiceRecording() { return recording; }
int voiceLevel() { return level; }

static void setBusy(bool b) {
  femiLock();
  femiView().busy = b;
  femiUnlock();
}

bool voiceStart(const char *trigger) {
  if (recording || !netUp()) return false;
  sumSq = 0;
  frameN = frames = speechFrames = speechRun = silentFrames = 0;
  skip = CHIME_SKIP;
  noise = 500;
  heard = false;
  char j[64];
  snprintf(j, sizeof j, "{\"rate\":16000,\"trigger\":\"%s\"}", trigger);
  netSend(F_AUDIO_START, j);   // queued before any audio, so the brain sees START first
  setBusy(true);
  listening = true;
  recording = true;
  playerChime(true);
  return true;
}

static void finish(const char *reason) {
  recording = false;
  listening = false;
  char j[48];
  snprintf(j, sizeof j, "{\"reason\":\"%s\"}", reason);
  netSend(F_AUDIO_END, j);   // the link sends every recorded byte before this
  if (strcmp(reason, "cancel") == 0) setBusy(false);
}

void voiceFeed(const int16_t *s, size_t n) {
  if (!recording) return;
  if (skip > 0) {
    size_t d = min(n, (size_t)skip);
    skip -= d;
    s += d;
    n -= d;
    if (n == 0) return;
  }
  mic.write(s, n * 2);
  for (size_t i = 0; i < n && recording; i++) {
    sumSq += (int32_t)s[i] * s[i];
    if (++frameN < FRAME) continue;
    int rms = (int)sqrtf((float)(sumSq / FRAME));
    sumSq = 0;
    frameN = 0;
    frames++;
    level = rms;
    if (rms > max(SPEECH_MIN, (int)(noise * 2))) {
      speechRun++;
    } else {
      speechRun = 0;
      noise += (rms - noise) * (rms < noise ? 0.1f : 0.03f);   // the room's typical noise, not its quietest moment
    }
    if (speechRun >= SPEECH_RUN) {
      silentFrames = 0;
      if (++speechFrames >= 4) heard = true;   // ~0.2 s of voice: someone is talking
    } else {
      silentFrames++;
    }
    if (heard && silentFrames >= END_SILENT_FRAMES) finish("silence");
    else if (frames >= MAX_FRAMES) finish("max");
    else if (!heard && frames >= GIVE_UP_FRAMES) finish("cancel");
  }
}

// ================= playback =================

static Ring spk;
static I2SClass amp;
static volatile bool playing = false, sayEnded = false;
static volatile int playLevel = 0;
static volatile int chimeReq = 0;   // 0 none, 1 happy, 2 soft
static char sayId[16];

Ring &speakRing() { return spk; }
bool playerBusy() { return playing; }
int playerLevel() { return playLevel; }
void playerChime(bool happy) { chimeReq = happy ? 1 : 2; }

void playerSayStart(const char *id, uint32_t bytes) {
  strlcpy(sayId, id, sizeof sayId);
  sayEnded = false;
  playing = true;
}

void playerSayEnd() { sayEnded = true; }

static void playChime(int16_t *buf, int kind) {
  const float notes[2][2] = {{660, 880}, {587, 440}};   // rising "ready" / falling "soft"
  for (int k = 0; k < 2; k++) {
    float phase = 0, step = 2 * PI * notes[kind - 1][k] / 16000;
    for (int left = 1600; left > 0; left -= 256) {   // 100 ms per note
      for (int i = 0; i < 256; i++) {
        float env = min(1.0f, (left - i) / 400.0f);    // quick fade at the end: no click
        buf[i] = (int16_t)(sinf(phase) * 5000 * SPEAKER_VOLUME * env);
        phase += step;
      }
      amp.write((uint8_t *)buf, 512);
    }
  }
}

static void playerTask(void *) {
  static int16_t buf[512];
  for (;;) {
    int c = chimeReq;
    if (c && spk.available() == 0) {
      chimeReq = 0;
      playChime(buf, c);
      continue;
    }
    size_t avail = spk.available() & ~(size_t)1;   // whole samples only
    size_t n = spk.read(buf, min(avail, sizeof buf));
    if (n > 0) {
      int count = n / 2;
      int64_t ss = 0;
      for (int i = 0; i < count; i++) {
        int32_t v = (int32_t)(buf[i] * SPEAKER_VOLUME);
        buf[i] = (int16_t)v;
        ss += v * v;
      }
      playLevel = (int)sqrtf((float)(ss / count));
      amp.write((uint8_t *)buf, n);
      continue;
    }
    playLevel = 0;
    if (playing && sayEnded) {   // the whole reply has played
      playing = false;
      bool followUp;
      femiLock();
      FemiView &v = femiView();
      v.speaking = false;
      size_t len = strlen(v.caption);
      followUp = len > 0 && v.caption[len - 1] == '?';   // Femi asked something: listen for the answer
      v.busy = false;
      femiUnlock();
      char j[64];
      snprintf(j, sizeof j, "{\"type\":\"played\",\"id\":\"%s\"}", sayId);
      netSend(F_EVENT, j);
      if (followUp) voiceStart("followup");
    }
    memset(buf, 0, 512);   // keep the amp fed with silence (~16 ms) so it never repeats old audio
    amp.write((uint8_t *)buf, 512);
  }
}

void playerBegin() {
  mic.begin(64 * 1024);        // 2 s of recording backlog
  spk.begin(512 * 1024);       // 16 s of reply audio
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  // mono sound on BOTH channels: the amp plays left+right averaged, so left-only would be half as loud
  if (!amp.begin(I2S_MODE_STD, 16000, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH)) Serial.println("amp I2S FAILED");
  xTaskCreatePinnedToCore(playerTask, "player", 4096, nullptr, 4, nullptr, 0);
}
