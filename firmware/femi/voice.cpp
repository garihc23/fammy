#include <ESP_I2S.h>
#include "voice.h"
#include "net.h"
#include "femi.h"
#include "shared.h"
#include "config.h"
#include "clips.h"

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
  if (strcmp(reason, "cancel") == 0) {
    setBusy(false);
    playerChime(false);
  } else {
    playerClipOf(CLIP_ACK, CLIP_ACK_N);   // "Okay!" straight away: no silent wait while the brain works
  }
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
static char sayId[16];

// sound cues wait in a queue and play between replies, never over one
struct Cue {
  int16_t code;   // an Sfx, or CLIP_BASE + a clip
  uint32_t at;    // when it was asked for: a cue that waited too long has missed its moment
};
static const int CLIP_BASE = 100;
static const uint32_t CUE_STALE_MS = 2000;
static QueueHandle_t cues;
static volatile bool cueing = false;

Ring &speakRing() { return spk; }
bool playerBusy() { return playing; }
int playerLevel() { return playLevel; }
bool playerQuiet() { return !playing && !cueing && uxQueueMessagesWaiting(cues) == 0 && spk.available() == 0; }

static void cue(int code) {
  Cue c = {(int16_t)code, millis()};
  xQueueSend(cues, &c, 0);
}
void playerSfx(Sfx s) { cue(s); }
void playerChime(bool happy) { cue(happy ? SFX_READY : SFX_SOFT); }
void playerClip(int clip) {
  if (clip >= 0 && clip < CLIP_COUNT) cue(CLIP_BASE + clip);
}
void playerClipOf(int first, int count) {   // the next of a group of clips, so Famyy doesn't repeat itself
  static uint8_t turn[CLIP_COUNT];
  playerClip(first + turn[first]++ % count);
}

void playerSayStart(const char *id, uint32_t bytes) {
  strlcpy(sayId, id, sizeof sayId);
  sayEnded = false;
  playing = true;
}

void playerSayEnd() { sayEnded = true; }

// ---------- sound effects, made on the spot ----------

static int16_t fx[256];

// one note: a sine (plus some of the octave above, for bells), a 3 ms rise so it doesn't click,
// then either a gentle exponential ring-out (decayMs) or a flat note that fades at the end
static void tone(float hz, int ms, float level, float decayMs, float octave = 0) {
  int n = 16 * ms;
  float ph = 0, step = 2 * PI * hz / 16000;
  for (int i = 0; i < n; i += 256) {
    int m = min(256, n - i);
    for (int j = 0; j < m; j++) {
      int k = i + j;
      float env = min(1.0f, k / 48.0f);
      env *= decayMs > 0 ? expf(-k / (16.0f * decayMs)) : min(1.0f, (n - k) / 400.0f);
      fx[j] = (int16_t)((sinf(ph) + octave * sinf(2 * ph)) * level * env * SPEAKER_VOLUME);
      ph += step;
    }
    amp.write((uint8_t *)fx, m * 2);
  }
}

static void quiet(int ms) {
  memset(fx, 0, sizeof fx);
  for (int n = 16 * ms; n > 0; n -= 256) amp.write((uint8_t *)fx, min(256, n) * 2);
}

// air rushing past: noise through a filter that opens up and closes again
static void whoosh(int ms, float level) {
  static uint32_t seed = 12345;
  int n = 16 * ms;
  float lp = 0;
  for (int i = 0; i < n; i += 256) {
    int m = min(256, n - i);
    for (int j = 0; j < m; j++) {
      float u = (float)(i + j) / n;
      seed = seed * 1664525 + 1013904223;
      float white = (int32_t)seed / 2147483648.0f;
      lp += (0.04f + 0.4f * sinf(PI * u)) * (white - lp);
      fx[j] = (int16_t)(lp * level * sinf(PI * u) * SPEAKER_VOLUME);
    }
    amp.write((uint8_t *)fx, m * 2);
  }
}

static void playSfx(int s) {
  switch (s) {
    case SFX_READY:  tone(660, 100, 5000, 0); tone(880, 100, 5000, 0); break;    // rising: I'm listening
    case SFX_SOFT:   tone(587, 100, 5000, 0); tone(440, 100, 5000, 0); break;    // falling: never mind
    case SFX_TICK:   tone(1760, 14, 900, 0); quiet(45); tone(2350, 12, 700, 0); break;   // tk-tk: still thinking
    case SFX_WHOOSH: whoosh(320, 9000); break;                                    // Fo comes or goes
    case SFX_CLINK:  tone(1568, 260, 3800, 70, 0.5f); break;                      // the handshake
    case SFX_DING:   tone(1047, 90, 4200, 120, 0.4f); tone(1568, 480, 4200, 160, 0.4f); break;   // done!
  }
}

// a stored clip, moving the mouth as it plays
static void playClip(const Clip &c) {
  for (uint32_t i = 0; i < c.n; i += 256) {
    int m = min((uint32_t)256, c.n - i);
    int64_t ss = 0;
    for (int j = 0; j < m; j++) {
      int32_t v = (int32_t)(c.pcm[i + j] * SPEAKER_VOLUME);
      fx[j] = (int16_t)v;
      ss += v * v;
    }
    playLevel = (int)sqrtf((float)(ss / m));
    amp.write((uint8_t *)fx, m * 2);
  }
  playLevel = 0;
}

static void playerTask(void *) {
  static int16_t buf[512];
  for (;;) {
    Cue q;
    if (!playing && spk.available() == 0 && xQueueReceive(cues, &q, 0) == pdTRUE) {
      if (millis() - q.at < CUE_STALE_MS) {
        cueing = true;
        if (q.code >= CLIP_BASE) playClip(CLIPS[q.code - CLIP_BASE]);
        else playSfx(q.code);
        cueing = false;
      }
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
  cues = xQueueCreate(8, sizeof(Cue));
  mic.begin(64 * 1024);        // 2 s of recording backlog
  spk.begin(512 * 1024);       // 16 s of reply audio
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  // mono sound on BOTH channels: the amp plays left+right averaged, so left-only would be half as loud
  if (!amp.begin(I2S_MODE_STD, 16000, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH)) Serial.println("amp I2S FAILED");
  xTaskCreatePinnedToCore(playerTask, "player", 4096, nullptr, 4, nullptr, 0);
}
