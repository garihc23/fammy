// Renders Famyy's screen on a laptop, with the device's own drawing code, so animations can be
// checked (and shown off) before flashing. Writes PPM frames; preview.sh turns them into a GIF.
//
//   ./preview.sh fo        Fo walks in, shakes hands, works, hands over the result, leaves
//   ./preview.sh shrug     the same when Fo couldn't do it
//   ./preview.sh icons     Fo working on each kind of job
//   ./preview.sh faces     every one of Famyy's moods
#include <Adafruit_GFX.h>
#include "../femi/creature.h"
#include "../femi/fo.h"

static constexpr uint16_t rgb565(uint8_t r, uint8_t g, uint8_t b) {
  return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3);
}
// the NORMAL (family) theme from face.cpp
static const Palette PAL = {rgb565(255, 246, 232), rgb565(43, 36, 32), rgb565(150, 138, 124), rgb565(228, 214, 196),
                            rgb565(255, 178, 138), rgb565(214, 98, 74), rgb565(232, 140, 104), rgb565(255, 160, 160),
                            rgb565(70, 170, 100), rgb565(240, 120, 80), rgb565(255, 255, 255), rgb565(43, 36, 32)};
static const int STAGE_Y = 24, TERM_Y = 204;

static GFXcanvas16 screen(320, 240), stage(320, 172), term(320, 36);
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

struct Drop { int x; float y, v; };
static Drop rain[18];

// one whole frame: status bar, the stage, the terminal line (typed out like face.cpp does)
static void frame(uint32_t t, Face f, int talk, FoState fo, uint32_t calledAt, uint32_t since, FoTask task,
                  const char *line, uint32_t lineSince) {
  stage.fillScreen(PAL.bg);
  stage.drawFastHLine(0, GROUND_Y + 1, 320, PAL.soft);   // the floor
  float gx = (f == THINKING || f == WORKING) ? 16 : 0, gy = (f == THINKING || f == WORKING) ? -12 : 0;
  Look k = {160, f, (t % 3900) < 200 ? 0.1f : 1.0f, gx, gy, talk, f == LISTENING, 0, 0};
  FoPose p = foPose(t, fo, calledAt, since);
  foPlaceFamyy(p, k);
  drawFamyy(&stage, t, k, PAL);
  foDraw(&stage, t, p, task, PAL);

  screen.fillScreen(PAL.bg);
  screen.setTextSize(1);
  screen.setTextColor(PAL.dim);
  screen.setCursor(10, 8);
  screen.print("Famyy");
  screen.drawRGBBitmap(0, STAGE_Y, stage.getBuffer(), 320, 172);
  term.fillScreen(PAL.bg);
  term.setTextSize(2);
  term.setTextColor(PAL.ink);
  term.setCursor(8, 10);
  char shown[48];
  snprintf(shown, sizeof shown, "%.*s", (int)min((t - lineSince) / 18 + 1, strlen(line)), line);
  term.print(shown);
  if ((t / 500) % 2) term.print("_");
  screen.drawRGBBitmap(0, TERM_Y, term.getBuffer(), 320, 36);
  save();
}

int main(int argc, char **argv) {
  if (argc < 3) {
    fprintf(stderr, "usage: preview fo|shrug|icons <out-dir>\n");
    return 2;
  }
  const char *what = argv[1];
  outDir = argv[2];
  for (int i = 0; i < 18; i++) rain[i] = {4 + i * 18, (float)((i * 53) % 200), 1.5f + (i * 7 % 25) / 10.0f};
  const uint32_t DT = 50;   // 20 frames a second

  if (!strcmp(what, "faces")) {   // every mood, a second each
    const Face all[] = {IDLE, LISTENING, THINKING, TALKING, HAPPY, ASKING, FOCUS, SLEEPING, ALARM};
    const char *names[] = {"idle", "listening", "thinking", "talking", "happy", "asking", "focus", "sleeping", "alarm"};
    for (int i = 0; i < 9; i++)
      for (uint32_t t = i * 1000; t < i * 1000 + 1000; t += DT) {
        int talk = all[i] == TALKING ? (int)(fabsf(sinf(t / 90.0f)) * 3500) : 0;
        frame(t, all[i], talk, FO_OFF, 0, 0, TASK_WEB, names[i], i * 1000);
      }
    return 0;
  }
  if (!strcmp(what, "icons")) {   // two seconds of Fo at work on each kind of job
    for (int task = TASK_WEB; task <= TASK_CALENDAR; task++)
      for (uint32_t t = 4000; t < 6000; t += DT) frame(t, WORKING, 0, FO_WORKING, 0, 0, (FoTask)task, "Fo is on it", 4000);
    return 0;
  }

  bool ok = strcmp(what, "shrug") != 0;
  const uint32_t heard = 600, called = 1600, done = 7000, talkAt = 9000, end = 11500;
  const char *ask = ok ? "book a haircut sat 11" : "order samosas, 2am";
  const char *reply = ok ? "Booked! Saturday 11am" : "Sorry, all closed now";
  for (uint32_t t = 0; t < end; t += DT) {
    if (t < heard)
      frame(t, IDLE, 0, FO_OFF, 0, 0, TASK_CALENDAR, "watching you code", 0);
    else if (t < called)
      frame(t, THINKING, 0, FO_OFF, 0, 0, TASK_CALENDAR, ask, heard);
    else if (t < done)
      frame(t, WORKING, 0, FO_WORKING, called, 0, ok ? TASK_CALENDAR : TASK_SHOP, t < called + 2600 ? "Asking Fo..." : "Fo: calling the salon", t < called + 2600 ? called : called + 2600);
    else if (t < talkAt)
      frame(t, THINKING, 0, ok ? FO_DONE : FO_FAILED, called, done, TASK_CALENDAR, ok ? "Fo is done" : "Fo couldn't", done);
    else {
      int talk = (int)(fabsf(sinf(t / 90.0f)) * 3500 * (0.5f + 0.5f * fabsf(sinf(t / 410.0f))));
      frame(t, TALKING, talk, ok ? FO_DONE : FO_FAILED, called, done, TASK_CALENDAR, reply, talkAt);
    }
  }
  return 0;
}
