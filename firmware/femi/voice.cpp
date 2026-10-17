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
static const int END_SILENT_MIN = 20;         // ~0.6 s of quiet ends a short request...
static const int END_SILENT_MAX = 44;         // ...and up to ~1.3 s a long one, where people pause to think
static const int MAX_FRAMES = 1000;           // 30 s at most
static const int GIVE_UP_FRAMES = 170;        // 5 s without speech: probably a false wake, drop it
static const int CHIME_SKIP = 16000 * 3 / 10;   // the first 0.3 s holds Famyy's own "listening" chime: never send or judge it
static int64_t sumSq;
static int skip, frameN, frames, speechFrames, speechRun, silentFrames;
static float noise;
static bool heard;
static bool answering;   // listening for an answer, not a new request: if nobody speaks, end quietly

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
  answering = !strcmp(trigger, "followup");
  char j[64];
  snprintf(j, sizeof j, "{\"rate\":16000,\"trigger\":\"%s\"}", trigger);
  netSend(F_AUDIO_START, j);   // queued before any audio, so the brain sees START first
  setBusy(true);
  femiLock();
  femiView().heard[0] = 0;     // a new request: forget what was heard last time, and how it went
  femiView().line[0] = 0;
  femiUnlock();
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
    if (!answering) playerChime(false);   // a false wake gets a soft "never mind"; an unanswered follow-up just ends
  } else {
    playerClipOf(CLIP_ACK, CLIP_ACK_N);   // "Okay!" straight away: no silent wait while the brain works
  }
}

void voiceCancel() {
  if (recording) finish("cancel");
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
    // the longer someone has been talking, the longer a pause they get before the request ends
    int endAfter = END_SILENT_MIN + constrain((speechFrames - 60) * (END_SILENT_MAX - END_SILENT_MIN) / 140, 0, END_SILENT_MAX - END_SILENT_MIN);
    if (heard && silentFrames >= endAfter) finish("silence");
    else if (frames >= MAX_FRAMES) finish("max");
    else if (!heard && frames >= GIVE_UP_FRAMES) finish("cancel");
  }
}

// ================= playback =================

static Ring spk;
static I2SClass amp;
static volatile bool playing = false, sayEnded = false;
static volatile bool stopReq = false, discarding = false;   // a tap cut the reply short
static volatile int sayListen = -1;                          // listen afterwards: 1 yes, 0 no, -1 only after a question
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

void playerSayStart(const char *id, uint32_t bytes, int listen) {
  strlcpy(sayId, id, sizeof sayId);
  sayListen = listen;
  sayEnded = false;
  discarding = false;
  playing = true;
}

void playerSayEnd() {
  if (!discarding) sayEnded = true;
}

void playerStop() {
  discarding = true;   // whatever is still on its way is dropped as it arrives
  stopReq = true;      // the player task empties what is already queued
}

bool playerDiscarding() { return discarding; }

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

// a quick rising sine: a bubbly pop
static void sweep(float from, float to, int ms, float level) {
  int n = 16 * ms;
  float ph = 0;
  for (int i = 0; i < n; i += 256) {
    int m = min(256, n - i);
    for (int j = 0; j < m; j++) {
      float u = (float)(i + j) / n;
      fx[j] = (int16_t)(sinf(ph) * level * sinf(PI * u) * SPEAKER_VOLUME);
      ph += 2 * PI * (from + (to - from) * u) / 16000;
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
    case SFX_DING: {                                                              // done! (in one of three keys)
      static const float keys[3][2] = {{1047, 1568}, {1175, 1760}, {988, 1480}};
      static int k = 0;
      k = (k + 1) % 3;
      tone(keys[k][0], 90, 4200, 120, 0.4f);
      tone(keys[k][1], 480, 4200, 160, 0.4f);
      break;
    }
    case SFX_POP:    sweep(320, 980, 70, 2600); break;                            // words appear in a bubble
    case SFX_SPARKLE:                                                             // caught it!
      for (float hz : {1568.0f, 1976.0f, 2349.0f, 3136.0f}) tone(hz, 70, 2200, 60, 0.3f);
      tone(4186, 260, 1600, 90, 0.2f);
      break;
    case SFX_JINGLE:                                                              // hello, I'm awake
      tone(784, 120, 3600, 140, 0.3f); tone(988, 120, 3600, 140, 0.3f); tone(1175, 120, 3600, 140, 0.3f);
      tone(1568, 420, 3800, 220, 0.4f);
      break;
    case SFX_CHIRP:                                                               // a happy little trill
      tone(1319, 55, 3200, 50, 0.2f); tone(1568, 55, 3200, 50, 0.2f); tone(2093, 120, 3400, 80, 0.3f);
      break;
    case SFX_QUESTION: tone(784, 110, 4000, 0); tone(1047, 220, 4000, 110, 0.3f); break;   // "hmm?": a yes or no, please
    case SFX_BELL:                                                                // ding-dong: a reminder
      tone(1568, 320, 4200, 130, 0.5f); quiet(60); tone(1245, 560, 4200, 200, 0.5f);
      break;
    case SFX_SHUTTER: whoosh(50, 9000); tone(2637, 160, 2600, 60, 0.3f); break;   // a photo
    case SFX_FLIP:   whoosh(70, 7000); quiet(40); whoosh(70, 5500); break;        // a page turns: a list
    case SFX_BOOP:   sweep(620, 410, 90, 2600); break;                            // a little remark pops up
    case SFX_YAWN:   sweep(280, 720, 450, 2600); sweep(720, 380, 350, 2200); break;   // waking up
    case SFX_LULLABY:                                                             // drifting off
      tone(784, 260, 2600, 160); tone(659, 260, 2400, 160); tone(523, 620, 2200, 260);
      break;
    case SFX_ALARM:                                                               // time's up, cheerfully
      for (float hz : {1047.0f, 1319.0f, 1568.0f, 1319.0f}) tone(hz, 110, 4400, 70, 0.3f);
      tone(2093, 260, 4400, 120, 0.3f);
      break;
    case SFX_ARCADE: sweep(400, 1600, 160, 3200); tone(1568, 80, 3600, 0); tone(2093, 170, 3800, 90, 0.2f); break;   // game on
    case SFX_GLITCH:                                                              // hacker mode: the signal breaks up
      for (int i = 0; i < 7; i++) tone(200 + esp_random() % 1800, 28, 2800, 0);
      whoosh(80, 6000);
      break;
    case SFX_TAP:    tone(2600, 12, 1800, 0); break;                              // a tiny tick under your finger
  }
}

// ---------- music while waiting ----------
// A slow tune made on the spot, so a long wait feels like a moment rather than a silence:
// a warm chord loop (Am F C G) underneath and a music-box melody wandering over a pentatonic scale.

static volatile bool musicWanted = false;
static float musicGain = 0;   // fades in and out
static uint32_t musicAt = 0;  // samples into the tune
static float sineTable[1025];

void playerMusic(bool on) { musicWanted = on; }

static inline float sine(float phase) {   // phase in turns: a table look-up, much cheaper than sinf
  float x = (phase - floorf(phase)) * 1024;
  int i = (int)x;
  return sineTable[i] + (sineTable[i + 1] - sineTable[i]) * (x - i);
}

static void musicChunk(int16_t *out, int n) {
  static const float CHORDS[4][3] = {{220.00f, 261.63f, 329.63f}, {174.61f, 220.00f, 261.63f},
                                     {196.00f, 261.63f, 329.63f}, {196.00f, 246.94f, 293.66f}};
  static const float SCALE[7] = {523.25f, 587.33f, 659.25f, 783.99f, 880.00f, 1046.50f, 1174.66f};
  static const int NOTE = 16000 * 3 / 8;   // a melody step every 0.375 s; a chord lasts 8 of them
  static float padPh[3], melPh, melEnv, melHz = 659.25f;
  static int degree = 2;
  for (int i = 0; i < n; i++, musicAt++) {
    float target = musicWanted ? MUSIC_VOLUME : 0;
    musicGain += (target - musicGain) * (musicWanted ? 0.00004f : 0.0004f);   // ~1.5 s in, ~0.15 s out
    if (musicAt % NOTE == 0) {   // the next melody note, or a rest
      uint32_t r = esp_random();
      if (r % 10 < 7) {
        degree = constrain(degree + (int)(r >> 8) % 5 - 2, 0, 6);
        melHz = SCALE[degree];
        melEnv = 1;
      }
    }
    const float *chord = CHORDS[(musicAt / (NOTE * 8)) % 4];
    float pad = 0;
    for (int k = 0; k < 3; k++) {
      padPh[k] += chord[k] / 16000;
      pad += sine(padPh[k]);
    }
    float swell = 0.75f + 0.25f * sine(musicAt / 96000.0f);   // the chord breathes, slowly
    melPh += melHz / 16000;
    melEnv *= 0.99975f;                                        // a music-box note rings out over ~0.25 s
    float mel = (sine(melPh) + 0.3f * sine(2 * melPh)) * melEnv;
    out[i] = (int16_t)((pad * 620 * swell + mel * 2400) * musicGain * SPEAKER_VOLUME);
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
    if (stopReq) {   // a tap cut the reply short: forget it and anything queued, no follow-up listening
      stopReq = false;
      spk.clear();
      xQueueReset(cues);
      playing = sayEnded = false;
      playLevel = 0;
      femiLock();
      femiView().speaking = false;
      femiView().busy = false;
      femiUnlock();
      cue(CLIP_BASE + CLIP_STOPPED);   // "Okay, I'll stop."
    }
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
      // keep the conversation going without the wake phrase when the brain says so (an older brain: after a question)
      followUp = sayListen >= 0 ? sayListen == 1 : len > 0 && v.caption[len - 1] == '?';
      v.busy = false;
      femiUnlock();
      char j[64];
      snprintf(j, sizeof j, "{\"type\":\"played\",\"id\":\"%s\"}", sayId);
      netSend(F_EVENT, j);
      if (followUp) voiceStart("followup");
    }
    if (musicWanted || musicGain > 0.001f) {
      musicChunk(buf, 256);   // music while waiting, instead of silence
    } else {
      memset(buf, 0, 512);    // keep the amp fed with silence (~16 ms) so it never repeats old audio
      musicAt = 0;            // the tune starts from the top next time
    }
    amp.write((uint8_t *)buf, 512);
  }
}

void playerBegin() {
  cues = xQueueCreate(8, sizeof(Cue));
  for (int i = 0; i <= 1024; i++) sineTable[i] = sinf(2 * PI * i / 1024);
  mic.begin(64 * 1024);        // 2 s of recording backlog
  spk.begin(512 * 1024);       // 16 s of reply audio
  amp.setPins(AMP_BCLK, AMP_LRC, AMP_DIN);
  // mono sound on BOTH channels: the amp plays left+right averaged, so left-only would be half as loud
  if (!amp.begin(I2S_MODE_STD, 16000, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH)) Serial.println("amp I2S FAILED");
  xTaskCreatePinnedToCore(playerTask, "player", 4096, nullptr, 4, nullptr, 0);
}
