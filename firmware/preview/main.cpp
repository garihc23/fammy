// Renders Famyy's screen on a laptop, with the device's own drawing code, so animations can be
// checked (and shown off) before flashing. Writes PPM frames; preview.sh turns them into a GIF.
//
//   ./preview.sh talk      listening, a thought cloud with what was heard, the reply in a speech bubble
//   ./preview.sh fo        Fo walks in, shakes hands, works, hands over the result, leaves
//   ./preview.sh shrug     the same when Fo couldn't do it
//   ./preview.sh icons     Fo working on each kind of job
//   ./preview.sh faces     every one of Famyy's moods
#include <Adafruit_GFX.h>
#include <Fonts/FreeSans9pt7b.h>
#include "../femi/QuicksandBold16.h"
#include "../femi/QuicksandMedium13.h"
#include "../femi/creature.h"
#include "../femi/fo.h"
#include "../femi/words.h"
#include "../femi/claude.h"

static constexpr uint16_t rgb565(uint8_t r, uint8_t g, uint8_t b) {
  return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3);
}
// the NORMAL (home) theme from face.cpp
static const Palette PAL = {rgb565(255, 246, 232), rgb565(43, 36, 32), rgb565(150, 138, 124), rgb565(228, 214, 196),
                            rgb565(255, 178, 138), rgb565(200, 86, 64), rgb565(232, 140, 104), rgb565(255, 160, 160),
                            rgb565(70, 170, 100), rgb565(240, 120, 80), rgb565(255, 255, 255), rgb565(43, 36, 32),
                            rgb565(255, 250, 240)};
static const uint16_t CLAUDE = rgb565(217, 119, 87);
static BubbleStyle BUBBLE = {0xFFFF, PAL.ink, PAL.ink, &QuicksandBold16};
static const int STAGE_Y = 24, STAGE_H = 216;

static GFXcanvas16 screen(320, 240), stage(320, STAGE_H), bar(320, 22);

static const char *outDir;
static int frameNo = 0;

static void save() {
  char path[512];
  snprintf(path, sizeof path, "%s/f%04d.ppm", outDir, frameNo++);
  FILE *f = fopen(path, "wb");
  fprintf(f, "P6\n320 240\n255\n");
  const uint16_t *px = screen.getBuffer();
  for (int i = 0; i < 320 * 240; i++) {
    uint16_t p = px[i];
    uint8_t rgb[3] = {(uint8_t)((p >> 11) << 3), (uint8_t)(((p >> 5) & 0x3F) << 2), (uint8_t)((p & 0x1F) << 3)};
    fwrite(rgb, 1, 3, f);
  }
  fclose(f);
}

// what the device would know this frame
struct Moment {
  Face f;
  int talk;                 // loudness of Famyy's voice
  FoState fo;
  uint32_t calledAt, foSince;
  FoTask task;
  Words w;
  float meter;              // listening loudness, or -1
  const char *say;
};

static float homeX = HOME_X;
static bool claudeOn = false;
static uint32_t claudeSince = 0, claudeUntil = 0;
static char shownStatus[96];
static uint32_t statusSince = 0;

// face.cpp's drawStage, minus the display
static void frame(uint32_t t, const Moment &m) {
  stage.fillScreen(PAL.bg);
  stage.drawFastHLine(0, GROUND_Y + 1, 320, PAL.soft);
  const char *status = m.w.status ? m.w.status : "";
  if (strncmp(status, shownStatus, sizeof shownStatus - 1) != 0) {
    strlcpy(shownStatus, status, sizeof shownStatus);
    statusSince = t;
  }
  Scene sc = {m.f, &m.w, nullptr, (int)homeX, statusSince, m.meter, m.say};
  FoPose p = foPose(t, m.fo, m.calledAt, m.foSince, (int)homeX);
  sc.p = &p;
  const char *text = nullptr;
  int n = 0;
  Side side = sideWords(t, sc, &text, &n);
  homeX += ((side != SIDE_NONE ? HOME_X_BESIDE : HOME_X) - homeX) * 0.18f;
  p = foPose(t, m.fo, m.calledAt, m.foSince, (int)homeX);
  sc.homeX = (int)homeX;

  float gx = (m.f == THINKING || m.f == WORKING) ? 16 : (m.f == TALKING ? 8 : 0);
  float gy = (m.f == THINKING || m.f == WORKING) ? -12 : (m.f == TALKING ? -3 : 0);
  Look k = {(int)homeX, m.f, (t % 3900) < 200 ? 0.1f : 1.0f, gx, gy, m.talk, m.f == LISTENING, 0, 0, &QuicksandBold16};
  foPlaceFamyy(p, k);
  drawFamyy(&stage, t, k, PAL);
  bool waiting = m.f == THINKING || m.f == WORKING;   // as femi.ino: busy, not talking, not recording
  if (waiting != claudeOn) {
    claudeOn = waiting;
    if (waiting) claudeSince = t, claudeUntil = 0;
    else claudeUntil = t;
  }
  int rx, ry;
  famyyRoofTop(k, t, &rx, &ry);
  drawClaude(&stage, t, claudePose(t, claudeOn, claudeSince, claudeUntil, p.phase != PH_NONE), rx, ry + 3, CLAUDE, PAL.ink);
  foDraw(&stage, t, p, m.task, PAL);
  drawWords(&stage, t, sc, side, text, n, BUBBLE);

  screen.fillScreen(PAL.bg);
  const char *clock = "2:51 am   Sun 18 Oct";
  bar.fillScreen(PAL.bg);
  bar.setFont(&QuicksandMedium13);
  bar.setTextColor(PAL.dim);
  bar.setCursor((320 - textWidth(&QuicksandMedium13, clock, strlen(clock))) / 2, 15);
  bar.print(clock);
  bar.setFont(nullptr);
  screen.drawRGBBitmap(0, 0, bar.getBuffer(), 320, 22);
  screen.drawRGBBitmap(0, STAGE_Y, stage.getBuffer(), 320, STAGE_H);
  save();
}

static int voice(uint32_t t) { return (int)(fabsf(sinf(t / 90.0f)) * 3500 * (0.5f + 0.5f * fabsf(sinf(t / 410.0f)))); }

int main(int argc, char **argv) {
  if (argc < 3) {
    fprintf(stderr, "usage: preview talk|fo|shrug|icons|faces <out-dir>\n");
    return 2;
  }
  const char *what = argv[1];
  outDir = argv[2];
  const uint32_t DT = 50;   // 20 frames a second
  Words none = {"", 0, "", "", 0, 1};

  if (!strcmp(what, "faces")) {   // every mood, a second each
    const Face all[] = {IDLE, LISTENING, THINKING, TALKING, HAPPY, ASKING, FOCUS, SLEEPING, ALARM};
    const char *says[] = {"home sweet home", nullptr, nullptr, nullptr, nullptr, "how long? (min)", "focus 24:12", nullptr, nullptr};
    for (int i = 0; i < 9; i++)
      for (uint32_t t = i * 1000; t < i * 1000 + 1000; t += DT)
        frame(t, {all[i], all[i] == TALKING ? voice(t) : 0, FO_OFF, 0, 0, TASK_WEB, none, all[i] == LISTENING ? 0.7f : -1, says[i]});
    return 0;
  }
  if (!strcmp(what, "icons")) {   // two seconds of Fo at work on each kind of job
    Words w = {"", 0, "Fo is on it", "", 0, 1};
    for (int task = TASK_WEB; task <= TASK_CALENDAR; task++)
      for (uint32_t t = 4000; t < 6000; t += DT) frame(t, {WORKING, 0, FO_WORKING, 0, 0, (FoTask)task, w, -1, nullptr});
    return 0;
  }
  if (!strcmp(what, "talk")) {   // a plain question: listen, think, answer
    const char *heard = "What's a fun fact about elephants that my kids would love?";
    const char *reply = "Elephants can hear with their feet! They feel rumbles through the ground from miles away, "
                        "so a baby elephant can tell when its mum is calling.";
    uint32_t heardAt = 2500, talkAt = 5500, talkMs = 9000;
    for (uint32_t t = 0; t < talkAt + talkMs + 1500; t += DT) {
      if (t < 800) frame(t, {IDLE, 0, FO_OFF, 0, 0, TASK_WEB, none, -1, nullptr});
      else if (t < heardAt) frame(t, {LISTENING, 0, FO_OFF, 0, 0, TASK_WEB, none, 0.3f + 0.7f * fabsf(sinf(t / 300.0f)), nullptr});
      else if (t < talkAt) frame(t, {THINKING, 0, FO_OFF, 0, 0, TASK_WEB, {heard, heardAt, heard, "", 0, 1}, -1, nullptr});
      else if (t < talkAt + talkMs) frame(t, {TALKING, voice(t), FO_OFF, 0, 0, TASK_WEB, {heard, heardAt, "", reply, talkAt, talkMs}, -1, nullptr});
      else frame(t, {IDLE, 0, FO_OFF, 0, 0, TASK_WEB, none, -1, nullptr});
    }
    return 0;
  }

  // a job for Fo: think, Fo's visit, the answer
  bool ok = strcmp(what, "shrug") != 0;
  const char *heard = ok ? "Book me a haircut on Saturday at 11" : "Order some samosas, it's 2 am";
  const char *reply = ok ? "Done! You're booked at the salon on Saturday at eleven." : "Sorry, nothing's open that late. Shall I try in the morning?";
  const uint32_t heardAt = 300, called = 2500, done = 9000, talkAt = 11000, talkMs = 4500, end = talkAt + talkMs + 1000;
  for (uint32_t t = 0; t < end; t += DT) {
    FoTask task = ok ? TASK_CALL : TASK_SHOP;
    if (t < called)
      frame(t, {THINKING, 0, FO_OFF, 0, 0, task, {heard, heardAt, heard, "", 0, 1}, -1, nullptr});
    else if (t < done)
      frame(t, {WORKING, 0, FO_WORKING, called, 0, task, {heard, heardAt, t < called + 4000 ? "Asking Fo..." : (ok ? "Fo: calling the salon" : "Fo: checking what's open"), "", 0, 1}, -1, nullptr});
    else if (t < talkAt)
      frame(t, {THINKING, 0, ok ? FO_DONE : FO_FAILED, called, done, task, {heard, heardAt, ok ? "Fo is done" : "Fo couldn't", "", 0, 1}, -1, nullptr});
    else if (t < talkAt + talkMs)
      frame(t, {TALKING, voice(t), ok ? FO_DONE : FO_FAILED, called, done, task, {heard, heardAt, "", reply, talkAt, talkMs}, -1, nullptr});
    else
      frame(t, {IDLE, 0, FO_OFF, 0, 0, task, none, -1, nullptr});
  }
  return 0;
}
