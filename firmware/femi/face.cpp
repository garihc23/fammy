#include <SPI.h>
#include <Adafruit_GFX.h>
#include <Adafruit_ST7789.h>
#include "face.h"
#include "shared.h"
#include "timer.h"
#include "voice.h"
#include "creature.h"
#include "fo.h"
#include "words.h"
#include <Fonts/FreeSans9pt7b.h>
#include <Fonts/FreeSansBold9pt7b.h>

struct Drop { int x; float y, v; };
struct Theme {
  uint16_t bg, ink, dim, rain;        // faded shades are pre-mixed against the background: the display has no transparency
  uint16_t good, bad, warn, accent;   // colours for apps (games, morse) and small highlights
  uint16_t wall, roof, door, blush;   // Famyy, the little house
  uint16_t fo, foInk;                 // Fo
  uint16_t bubble;                    // inside speech bubbles and thought clouds
  bool hacker;                        // code rain and signal glitches
  const char *prompt, *prefix;        // top bar; what starts the bottom line
  const char *quips[5];
  const char *listening, *happy, *thinking, *sleeping;
  const char *focus, *brk, *done, *ask;
};

static constexpr uint16_t rgb565(uint8_t r, uint8_t g, uint8_t b) {
  return ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3);
}

// home: warm and light, line art like Wajo's Fo
static const Theme NORMAL = {
  rgb565(255, 246, 232), rgb565(43, 36, 32), rgb565(150, 138, 124), rgb565(228, 214, 196),
  rgb565(70, 170, 100), rgb565(220, 80, 70), rgb565(230, 150, 40), rgb565(240, 120, 80),
  rgb565(255, 178, 138), rgb565(214, 98, 74), rgb565(232, 140, 104), rgb565(255, 160, 160),
  rgb565(255, 255, 255), rgb565(43, 36, 32),
  rgb565(255, 255, 255),
  false,
  "Famyy", "",
  {"home sweet home", "say: hey Famyy", "tap me to talk", "here for the family", "all's well at home"},
  "listening...", "yay!", "thinking", "zzz",
  "focus", "break", "time's up!", "how long? (min)",
};
// hacking mode: the old green terminal, and the house drawn in green
static const Theme HACKER = {
  0x0000, rgb565(57, 255, 122), rgb565(20, 89, 43), rgb565(13, 56, 27),
  rgb565(57, 255, 122), rgb565(200, 255, 200), rgb565(30, 170, 80), rgb565(120, 255, 170),
  rgb565(6, 40, 18), rgb565(20, 110, 55), rgb565(10, 70, 32), rgb565(30, 150, 70),
  rgb565(200, 255, 215), rgb565(57, 255, 122),
  0x0000,
  true,
  "root@famyy:~#", "> ",
  {"sudo make coffee", "nmap -sV localhost", "tail -f /var/log/life", "ssh famyy@home", "chmod +x brain.sh"},
  "sniffing audio", "access granted :)", "cracking", "halt",
  "exploit", "cooldown", "pwned! time up", "enter duration:",
};
static const Theme *th = &NORMAL;

// screen zones, each drawn off-screen and pushed in one go
static const int BODY_X = 50, BODY_Y = 24, BODY_W = 220, BODY_H = 172;   // apps and cards: the middle of the screen
static const int STAGE_Y = 24, STAGE_H = 216;                             // the face: the whole screen under the top bar
static const int TERM_Y = 204, TERM_H = 36;

static Adafruit_ST7789 tft(&SPI, TFT_CS, TFT_DC, TFT_RST);
static GFXcanvas16 *body, *stage, *term;   // allocated in faceBegin(), once the extra memory (PSRAM) is ready
static Drop rain[18];
static bool stageShown = false;            // the full-width stage is on screen (cards and apps must clear its sides)

// Fo's scene, as the brain last described it
static FoState foState = FO_OFF;
static uint32_t foCalledAt = 0, foSince = 0;
static FoTask foTask = TASK_WEB;

// words in bubbles
static float homeX = HOME_X;
static char shownStatus[96];
static uint32_t statusSince = 0;

// animation state
static float gazeX = 0, gazeY = 0, gazeTX = 0, gazeTY = 0;
static uint32_t nextGaze = 0, nextBlink = 2000, blinkStart = 0, nextGlitch = 4000, glitchUntil = 0;
// what is on screen now, so each zone is only redrawn when it changes
static uint32_t lastTerm = 0;
static int shownTimerKey = -1;
static bool shownRec = false, shownDot = false, shownInvert = false, shownFps = false;

// ---------- drawing helpers ----------

// signal glitch: slide a couple of horizontal strips sideways
static void glitch(GFXcanvas16 *c) {
  uint16_t *buf = c->getBuffer();
  int w = c->width();
  for (int b = 0; b < 2; b++) {
    int y0 = random(c->height() - 10), h = random(3, 10);
    int dx = random(4, 11) * (random(2) ? 1 : -1);
    for (int row = y0; row < y0 + h; row++) {
      uint16_t *r = buf + row * w;
      if (dx > 0) { memmove(r + dx, r, (w - dx) * 2); memset(r, 0, dx * 2); }
      else        { memmove(r, r - dx, (w + dx) * 2); memset(r + w + dx, 0, -dx * 2); }
    }
  }
  c->drawFastHLine(0, random(c->height()), w, th->dim);
}

static float blinkAmount(uint32_t now) {
  uint32_t bt = now - blinkStart;
  return bt < 220 ? max(0.05f, 1.0f - sinf(PI * bt / 220.0f)) : 1.0f;
}

// ---------- screen zones ----------

static Palette palette() {
  return {th->bg, th->ink, th->dim, th->rain, th->wall, th->roof, th->door, th->blush, th->good, th->accent, th->fo, th->foInk};
}

static BubbleStyle bubbleStyle() { return {th->bubble, th->ink, th->ink, &FreeSans9pt7b}; }

static void drawStage(uint32_t t, Face f, float blink, const Words &w) {
  GFXcanvas16 *c = stage;
  c->fillScreen(th->bg);
  c->drawFastHLine(0, GROUND_Y + 1, 320, th->rain);   // the floor

  // code rain, in hacking mode
  if (th->hacker) {
    c->setTextSize(1);
    for (auto &d : rain) {
      d.y += d.v;
      if (d.y > STAGE_H + 50) d.y = -10;
      for (int k = 0; k < 5; k++) {
        c->setTextColor(k == 0 && f == THINKING ? th->dim : th->rain);
        c->setCursor(d.x, (int)d.y - k * 10);
        c->print("01abcdef"[((int)d.y / 10 + k * 3 + d.x) & 7]);
      }
    }
  }

  // a status line that changes starts typing again
  const char *status = w.status ? w.status : "";
  if (strncmp(status, shownStatus, sizeof shownStatus - 1) != 0) {
    strlcpy(shownStatus, status, sizeof shownStatus);
    statusSince = t;
  }

  // small remarks for the top bubble: a timer, a question, now and then a quip
  char buf[32];
  const char *say = nullptr;
  if ((f == IDLE || f == FOCUS) && timerCounting()) {
    uint32_t secs = (timerLeft(t) + 999) / 1000;
    const char *label = timerPaused() ? "paused" : (timerPhase() == T_FOCUS ? th->focus : th->brk);
    snprintf(buf, sizeof buf, "%s %02lu:%02lu", label, (unsigned long)(secs / 60), (unsigned long)(secs % 60));
    say = buf;
  } else if (f == ASKING) {
    say = th->ask;
  } else if (f == IDLE && t > 15000 && t % 30000 < 5000) {
    say = th->quips[(t / 30000) % 5];
  }
  float meter = f != LISTENING ? -1 : voiceRecording() ? constrain((log10f(max(voiceLevel(), 100)) - 2.3f) / 1.7f, 0.1f, 1.0f) : 0.6f;

  // Famyy steps aside for a big bubble, and back when it's gone
  Scene sc = {f, &w, nullptr, (int)homeX, statusSince, meter, say};
  FoPose p = foPose(t, foState, foCalledAt, foSince, (int)homeX);
  sc.p = &p;
  const char *text = nullptr;
  int n = 0;
  Side side = sideWords(t, sc, &text, &n);
  homeX += ((side != SIDE_NONE ? HOME_X_BESIDE : HOME_X) - homeX) * 0.3f;
  p = foPose(t, foState, foCalledAt, foSince, (int)homeX);
  sc.homeX = (int)homeX;

  Look k = {(int)homeX, f, blink, gazeX, gazeY, playerLevel(), listening, 0, 0};
  foPlaceFamyy(p, k);
  Palette pal = palette();
  drawFamyy(c, t, k, pal);
  foDraw(c, t, p, foTask, pal);
  drawWords(c, t, sc, side, text, n, BubbleStyle{th->bubble, th->ink, th->ink, &FreeSans9pt7b});
  if (th->hacker && t < glitchUntil) glitch(c);

  tft.drawRGBBitmap(0, STAGE_Y, c->getBuffer(), 320, STAGE_H);
  stageShown = true;
}

// a card or an app draws only the middle of the screen: clear what the stage left around it
static void leaveStage() {
  if (!stageShown) return;
  tft.fillRect(0, STAGE_Y, 320, STAGE_H, th->bg);
  stageShown = false;
}

static void drawStatus() {
  tft.fillRect(0, 0, 200, 20, th->bg);
  tft.setTextSize(1);
  tft.setTextColor(th->dim);
  tft.setCursor(10, 8);
  tft.print(th->prompt);
}

// small countdown in the top bar, visible whatever mood the buddy is in
static void drawStatusTimer(uint32_t now) {
  bool on = timerCounting();
  int secs = on ? (timerLeft(now) + 999) / 1000 : 0;
  int key = on ? secs * 2 + timerPaused() : -1;
  if (key == shownTimerKey) return;
  tft.fillRect(200, 4, 56, 14, th->bg);
  if (on) {
    char buf[8];
    snprintf(buf, sizeof buf, "%02d:%02d", secs / 60, secs % 60);
    tft.setTextSize(1);
    tft.setTextColor(timerPaused() ? th->dim : th->ink);
    tft.setCursor(214, 8);
    tft.print(buf);
  }
  shownTimerKey = key;
}

static void drawRec(uint32_t now) {
  bool on = listening, dot = on && (now / 300) % 2 == 0;
  if (on == shownRec && dot == shownDot) return;
  tft.fillRect(258, 4, 62, 14, th->bg);
  if (on) {
    if (dot) tft.fillRect(262, 7, 8, 8, th->ink);
    else     tft.drawRect(262, 7, 8, 8, th->dim);
    tft.setTextSize(1);
    tft.setTextColor(th->ink);
    tft.setCursor(274, 8);
    tft.print("REC");
  }
  shownRec = on;
  shownDot = dot;
}

// ---------- public ----------

void faceBegin() {
  pinMode(SD_CS, OUTPUT);
  digitalWrite(SD_CS, HIGH);     // keep the SD card quiet on the shared bus
  pinMode(TFT_BLK, OUTPUT);
  digitalWrite(TFT_BLK, HIGH);   // backlight on

  SPI.begin(PIN_SCK, PIN_MISO, PIN_MOSI);
  tft.init(240, 320);
  tft.setSPISpeed(TFT_SPI_HZ);
  tft.invertDisplay(false);
  tft.setRotation(1);
  tft.fillScreen(th->bg);
  drawStatus();

  body = new GFXcanvas16(BODY_W, BODY_H);
  stage = new GFXcanvas16(320, STAGE_H);
  term = new GFXcanvas16(320, TERM_H);
  if (!body->getBuffer() || !stage->getBuffer() || !term->getBuffer()) Serial.println("canvas alloc FAILED");
  for (int i = 0; i < 18; i++) rain[i] = {4 + i * 18, (float)random(0, 200), 1.5f + random(0, 25) / 10.0f};
}

// theme switch: repaint everything in the new colours
static void applyTheme() {
  const Theme *want = hackerMode ? &HACKER : &NORMAL;
  if (want != th) {
    th = want;
    tft.fillScreen(th->bg);
    drawStatus();
    shownTimerKey = -2;
    shownRec = !listening;
  }
}

// frame rate while an app runs, in the top bar: shows what a faster display link buys
static void drawFps(uint32_t now) {
  static uint32_t frames = 0, since = 0;
  frames++;
  if (now - since < 500) return;
  char buf[12];
  snprintf(buf, sizeof buf, "%2lu fps", (unsigned long)(frames * 1000 / (now - since)));
  tft.fillRect(140, 4, 58, 14, th->bg);
  tft.setTextSize(1);
  tft.setTextColor(th->dim);
  tft.setCursor(150, 8);
  tft.print(buf);
  frames = 0;
  since = now;
  shownFps = true;
}

void faceAppFrame(uint32_t now, const App &app, float exitHold) {
  applyTheme();
  leaveStage();
  drawFps(now);
  uint32_t t0 = micros();
  app.draw(body, Ink{th->bg, th->ink, th->dim, th->rain, th->good, th->bad, th->warn, th->accent});
  uint32_t t1 = micros();
  tft.drawRGBBitmap(BODY_X, BODY_Y, body->getBuffer(), BODY_W, BODY_H);
  uint32_t t2 = micros();
  if (PROFILE) {   // averages: time drawing into memory vs sending to the display vs the whole frame
    static uint32_t draw = 0, push = 0, frames = 0, since = 0, prev = 0;
    static uint32_t whole = 0;
    if (prev) whole += t0 - prev;
    prev = t0;
    draw += t1 - t0;
    push += t2 - t1;
    if (++frames >= 2 && now - since >= 2000) {
      Serial.printf("frame %lu ms = draw %lu ms + send %lu ms + everything else (%lu fps)\n",
                    whole / (frames - 1) / 1000, draw / frames / 1000, push / frames / 1000,
                    (frames - 1) * 1000000UL / max(whole, 1UL));
      draw = push = whole = frames = 0;
      prev = 0;
      since = now;
    }
  }
  if (now - lastTerm >= 100 || exitHold > 0) {
    term->fillScreen(th->bg);
    term->setTextSize(2);
    term->setTextColor(th->ink);
    term->setCursor(8, 10);
    term->print(th->prefix);
    if (exitHold > 0) {   // holding the pad: show how close you are to leaving
      term->print("exit");
      for (int i = 0; i < 10; i++) {
        if (i < exitHold * 10) term->fillRect(80 + i * 12, 12, 9, 14, th->warn);
        else                   term->drawRect(80 + i * 12, 12, 9, 14, th->dim);
      }
    } else {
      char buf[32];
      app.status(buf, sizeof buf);
      term->print(buf);
    }
    tft.drawRGBBitmap(0, TERM_Y, term->getBuffer(), 320, TERM_H);
    lastTerm = now;
  }
  drawStatusTimer(now);
  drawRec(now);
}

void faceSetFo(FoState state, uint32_t calledAt, uint32_t since, FoTask task) {
  foState = state;
  foCalledAt = calledAt;
  foSince = since;
  foTask = task;
}

void faceFrame(uint32_t now, Face f, const Words &words) {
  applyTheme();
  if (shownFps) {   // an app just ended: clear its frame-rate readout
    tft.fillRect(140, 4, 58, 14, th->bg);
    shownFps = false;
  }

  // whole-screen flash while a finished timer is ringing
  bool inv = f == ALARM && (now / 400) % 2;
  if (inv != shownInvert) {
    tft.invertDisplay(inv);
    shownInvert = inv;
  }

  if (f != SLEEPING && f != LISTENING && f != ALARM && now > nextBlink) {
    blinkStart = now;
    nextBlink = now + (random(5) == 0 ? 400 : random(3000, 6001));   // 1 in 5: double blink
  }
  if (now > nextGaze) {
    gazeTX = random(-14, 15);
    gazeTY = random(-8, 9);
    nextGaze = now + random(1200, 3700);
  }
  float gx = gazeTX, gy = gazeTY;
  if (f == THINKING || f == WORKING) { gx = 16; gy = -12; }
  if (f == TALKING)                 { gx = 8;  gy = -3; }    // towards its speech bubble
  if (f == ASKING)                  { gx = 10; gy = -8; }   // glancing up, waiting for your answer
  if (f == FOCUS)                   { gx = 0;  gy = 6; }    // eyes down on the work
  if (f == LISTENING || f == ALARM) { gx = 0;  gy = 0; }
  gazeX += (gx - gazeX) * 0.25f;
  gazeY += (gy - gazeY) * 0.25f;

  if (now > nextGlitch) {
    glitchUntil = now + 140;
    nextGlitch = now + (th == &HACKER ? random(1500, 4000) : random(5000, 11000));
  }

  drawStage(now, f, blinkAmount(now), words);
  drawStatusTimer(now);
  drawRec(now);
}

// ---------- Femi's screens ----------

// word-wrap text into lines of at most maxChars, starting at row y; returns rows used
static int drawWrapped(GFXcanvas16 *c, const char *text, int y, int size, int maxChars, int maxLines) {
  const char *p = text;
  int rows = 0;
  while (*p && rows < maxLines) {
    while (*p == ' ') p++;
    int len = strlen(p), cut = min(len, maxChars);
    if (len > maxChars) {   // break at the last space that fits
      int sp = cut;
      while (sp > 0 && p[sp] != ' ') sp--;
      if (sp > 0) cut = sp;
    }
    char line[48];
    snprintf(line, sizeof line, "%.*s", cut, p);
    if (rows == maxLines - 1 && len > cut) {   // out of room: end with dots
      int l = strlen(line);
      strcpy(line + max(0, min(l, maxChars - 3)), "...");
    }
    c->setTextSize(size);
    c->setCursor((c->width() - (int)strlen(line) * 6 * size) / 2, y + rows * 9 * size);
    c->print(line);
    p += cut;
    rows++;
  }
  return rows;
}

static void drawHint(uint32_t now, const char *hint) {
  term->fillScreen(th->bg);
  term->setTextSize(2);
  term->setTextColor(th->ink);
  term->setCursor(8, 10);
  term->print(th->prefix);
  term->print(hint);
  if ((now / 500) % 2) term->print("_");
  tft.drawRGBBitmap(0, TERM_Y, term->getBuffer(), 320, TERM_H);
}

void faceCardFrame(uint32_t now, bool urgent, const char *title, const char *text, const char *hint, float hold) {
  applyTheme();
  leaveStage();
  GFXcanvas16 *c = body;
  c->fillScreen(th->bg);
  uint16_t edge = urgent ? (((now / 500) % 2) ? th->warn : th->bad) : th->ink;   // reminders pulse
  c->drawRoundRect(4, 4, BODY_W - 8, BODY_H - 8, 10, edge);
  c->drawRoundRect(5, 5, BODY_W - 10, BODY_H - 10, 9, edge);
  c->setTextColor(urgent ? th->warn : th->accent);
  drawWrapped(c, title, 16, 2, 16, 1);
  c->setTextColor(th->ink);
  drawWrapped(c, text, 46, 2, 16, 5);
  if (hold > 0) {   // holding the pad: how close to "no"
    c->drawRect(14, BODY_H - 22, BODY_W - 28, 8, th->dim);
    c->fillRect(14, BODY_H - 22, (int)((BODY_W - 28) * min(hold, 1.0f)), 8, th->bad);
  }
  tft.drawRGBBitmap(BODY_X, BODY_Y, c->getBuffer(), BODY_W, BODY_H);
  if (now - lastTerm >= 100) {
    drawHint(now, hint);
    lastTerm = now;
  }
  drawStatusTimer(now);
  drawRec(now);
}

// a list (recent calls, ...) on a card over the whole screen: a title, up to 8 rows; drawn once
void faceListFrame(uint32_t now, const char *title, const char *items, bool fresh) {
  if (!fresh) return;
  if (shownInvert) {
    tft.invertDisplay(false);
    shownInvert = false;
  }
  tft.fillScreen(th->bg);
  tft.fillRoundRect(6, 6, 308, 228, 14, th->ink);
  tft.fillRoundRect(8, 8, 304, 224, 12, th->bubble);
  tft.setFont(&FreeSansBold9pt7b);
  tft.setTextColor(th->ink);
  tft.setCursor(20, 30);
  tft.print(title);
  tft.fillRect(20, 38, 280, 2, th->accent);
  tft.setFont(nullptr);
  tft.setTextSize(1);
  tft.setTextColor(th->dim);
  tft.setCursor(232, 22);
  tft.print("tap to close");

  tft.setFont(&FreeSans9pt7b);
  int row = 0;
  for (const char *p = items; *p && row < 8; row++) {
    const char *bar = strchr(p, '|');
    int len = bar ? bar - p : strlen(p);
    int y = 46 + row * 22;
    if (row % 2) tft.fillRoundRect(14, y, 292, 21, 6, th->rain);   // soft stripes, easy to follow across
    while (len > 0 && textWidth(&FreeSans9pt7b, p, len) > 276) len--;   // too long for the card: cut
    char line[64];
    snprintf(line, sizeof line, "%.*s", min(len, (int)sizeof line - 1), p);
    tft.setTextColor(th->ink);
    tft.setCursor(22, y + 15);
    tft.print(line);
    p = bar ? bar + 1 : p + strlen(p);
  }
  tft.setFont(nullptr);
  stageShown = false;
}

void facePhotoFrame(uint32_t now, const uint16_t *pixels, const char *caption, bool fresh) {
  if (!fresh) return;   // a photo is drawn once and then left alone
  if (shownInvert) {
    tft.invertDisplay(false);
    shownInvert = false;
  }
  tft.drawRGBBitmap(0, 0, (uint16_t *)pixels, 320, 240);
  if (caption && caption[0]) {   // caption on a dark band along the bottom
    char line[27];
    snprintf(line, sizeof line, "%s", caption);
    tft.fillRect(0, 214, 320, 26, th->bg);
    tft.setTextSize(2);
    tft.setTextColor(th->ink);
    tft.setCursor((320 - (int)strlen(line) * 12) / 2, 220);
    tft.print(line);
  }
}

void faceRepaint() {
  tft.fillScreen(th->bg);
  stageShown = false;
  drawStatus();
  shownTimerKey = -2;
  shownRec = !listening;
  shownFps = false;
}
