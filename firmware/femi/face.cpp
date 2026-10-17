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
#include "claude.h"
#include <Fonts/FreeSans9pt7b.h>
#include <Fonts/FreeSansBold9pt7b.h>
#include "QuicksandBold16.h"
#include "QuicksandMedium13.h"
#include <time.h>

struct Drop { int x; float y, v; };
struct Theme {
  uint16_t bg, ink, dim, rain;        // faded shades are pre-mixed against the background: the display has no transparency
  uint16_t good, bad, warn, accent;   // colours for apps (games, morse) and small highlights
  uint16_t wall, roof, door, blush;   // Famyy, the little house
  uint16_t fo, foInk;                 // Fo
  uint16_t bubble;                    // inside speech bubbles and thought clouds
  uint16_t tag, tagFill, tagEdge;     // name chips on the floor: words, inside, outline
  uint16_t claude, claudeEyes;        // Claude, on Famyy's roof
  bool hacker;                        // code rain and signal glitches
  const char *prefix;                 // what starts a card's or an app's bottom line
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
  rgb565(255, 178, 138), rgb565(200, 86, 64), rgb565(232, 140, 104), rgb565(255, 160, 160),
  rgb565(255, 255, 255), rgb565(43, 36, 32),
  rgb565(255, 255, 255),
  rgb565(43, 36, 32), rgb565(255, 255, 255), rgb565(228, 214, 196),
  rgb565(217, 119, 87), rgb565(43, 36, 32),
  false,
  "",
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
  rgb565(57, 255, 122), 0x0000, rgb565(20, 89, 43),
  rgb565(217, 119, 87), 0x0000,
  true,
  "> ",
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
static GFXcanvas16 *body, *stage, *term, *bar;   // allocated in faceBegin(), once the extra memory (PSRAM) is ready
static Drop rain[18];
static bool stageShown = false;            // the full-width stage is on screen (cards and apps must clear its sides)

// Fo's scene, as the brain last described it
static FoState foState = FO_OFF;
static uint32_t foCalledAt = 0, foSince = 0;
static FoTask foTask = TASK_WEB;
static int foOut = 0;   // errands Fo is running in the background

// words in bubbles
static float homeX = HOME_X;
static char shownStatus[96];
static uint32_t statusSince = 0;

// animation state
static float gazeX = 0, gazeY = 0, gazeTX = 0, gazeTY = 0;
static uint32_t nextGaze = 0, nextBlink = 2000, blinkStart = 0, nextGlitch = 4000, glitchUntil = 0;
// what is on screen now, so each zone is only redrawn when it changes
static uint32_t lastTerm = 0;
static bool shownInvert = false, shownFps = false;

// the camera's snapshot, while it's being talked about
static const uint16_t *snapPixels = nullptr;
static uint32_t snapAt = 0;

// Claude on the roof: when Famyy started and stopped waiting on it
static bool claudeOn = false;
static uint32_t claudeSince = 0, claudeUntil = 0;

// the top bar
static bool linkUp = false;
static char fpsText[10] = "";   // an app's frame rate, shown on the right while it runs
static bool barDirty = true;

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

// the time as the clock shows it, once the internet has told the device what time it is
static bool clockText(char *hm, size_t hn, char *ap, size_t an, char *date, size_t dn) {
  struct tm tm;
  if (!getLocalTime(&tm, 0)) return false;
  int h = tm.tm_hour % 12;
  snprintf(hm, hn, "%d:%02d", h ? h : 12, tm.tm_min);
  snprintf(ap, an, "%s", tm.tm_hour < 12 ? "am" : "pm");
  strftime(date, dn, "%a %d %b", &tm);
  return true;
}

static Palette palette() {
  return {th->bg, th->ink, th->dim, th->rain, th->wall, th->roof, th->door, th->blush, th->good, th->accent, th->fo, th->foInk, th->tag, th->tagFill, th->tagEdge};
}

static BubbleStyle bubbleStyle() { return {th->bubble, th->ink, th->ink, &FreeSans9pt7b}; }

static void drawStage(uint32_t t, Face f, float blink, const Words &w) {
  GFXcanvas16 *c = stage;
  c->fillScreen(th->bg);

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
  Scene sc = {f, &w, nullptr, (int)homeX, statusSince, meter, say, snapPixels};
  FoPose p = foPose(t, foState, foCalledAt, foSince, (int)homeX);
  sc.p = &p;
  const char *text = nullptr;
  int n = 0;
  Side side = sideWords(t, sc, &text, &n);
  homeX += ((side != SIDE_NONE ? HOME_X_BESIDE : HOME_X) - homeX) * 0.3f;
  p = foPose(t, foState, foCalledAt, foSince, (int)homeX);
  sc.homeX = (int)homeX;

  Look k = {(int)homeX, f, blink, gazeX, gazeY, playerLevel(), listening, 0, 0, &QuicksandBold16};
  foPlaceFamyy(p, k);
  Palette pal = palette();
  drawFamyy(c, t, k, pal);
  int rx, ry;
  famyyRoofTop(k, t, &rx, &ry);
  drawClaude(c, t, claudePose(t, claudeOn, claudeSince, claudeUntil, p.phase != PH_NONE), rx, ry + 3, th->claude, th->claudeEyes);
  foDraw(c, t, p, foTask, pal, &QuicksandBold16);
  drawWords(c, t, sc, side, text, n, BubbleStyle{th->bubble, th->ink, th->ink, &QuicksandBold16});
  if (th->hacker && t < glitchUntil) glitch(c);
  if (snapPixels && t - snapAt < 160) c->fillScreen(0xFFFF);   // the camera's flash

  tft.drawRGBBitmap(0, STAGE_Y, c->getBuffer(), 320, STAGE_H);
  stageShown = true;
}

// a card or an app draws only the middle of the screen: clear what the stage left around it
static void leaveStage() {
  if (!stageShown) return;
  tft.fillRect(0, STAGE_Y, 320, STAGE_H, th->bg);
  stageShown = false;
}

// the top bar: a running timer on the left, the time and date quietly in the middle, REC (or an app's frame rate)
// on the right. Drawn off-screen and only when something in it changes.
static void drawBar(uint32_t now) {
  struct Shown {
    int minute, secs, foOut, spin;
    bool paused, rec, dot, online;
    char fps[10];
  };
  static Shown shown;
  bool counting = timerCounting();
  char hm[8], ap[4], date[24];
  bool known = clockText(hm, sizeof hm, ap, sizeof ap, date, sizeof date);
  struct tm tm;
  Shown st;
  memset(&st, 0, sizeof st);   // so whole-struct comparison sees no stray padding
  st.minute = known && getLocalTime(&tm, 0) ? tm.tm_hour * 60 + tm.tm_min : -1;
  st.online = linkUp;
  st.foOut = foOut;
  st.spin = foOut ? (now / 250) % 4 : 0;   // the badge's little dots turn while Fo is out
  st.secs = counting ? (timerLeft(now) + 999) / 1000 : -1;
  st.paused = counting && timerPaused();
  st.rec = listening;
  st.dot = listening && (now / 300) % 2 == 0;
  strlcpy(st.fps, fpsText, sizeof st.fps);
  if (!barDirty && memcmp(&st, &shown, sizeof st) == 0) return;

  bar->fillScreen(th->bg);
  if (known) {   // "2:51 am  Sun 18 Oct", small and calm: the time, not the star
    char line[40];
    snprintf(line, sizeof line, "%s %s   %s", hm, ap, date);
    int w = textWidth(&QuicksandMedium13, line, strlen(line));
    bar->setFont(&QuicksandMedium13);
    bar->setTextColor(linkUp ? th->dim : th->rain);   // fainter while the brain is out of reach
    bar->setCursor((320 - w) / 2, 15);
    bar->print(line);
    bar->setFont(nullptr);
  }
  bar->setTextSize(1);
  if (counting) {
    char buf[8];
    snprintf(buf, sizeof buf, "%02d:%02d", st.secs / 60, st.secs % 60);
    bar->setTextColor(st.paused ? th->dim : th->ink);
    bar->setCursor(10, 8);
    bar->print(buf);
  }
  if (st.foOut && !st.rec) {   // "Fo is out": a tiny Fo, three turning dots, and how many errands
    int x = 236;
    bar->fillRoundRect(x, 3, 15, 15, 6, th->foInk);
    bar->fillRoundRect(x + 1, 4, 13, 13, 5, th->fo);
    bar->fillRect(x + 4, 8, 2, 4, th->foInk);
    bar->fillRect(x + 9, 8, 2, 4, th->foInk);
    char label[12];
    snprintf(label, sizeof label, st.foOut > 1 ? "out x%d" : "out", st.foOut);
    bar->setFont(&QuicksandMedium13);
    bar->setTextColor(th->dim);
    bar->setCursor(x + 19, 15);
    bar->print(label);
    bar->setFont(nullptr);
    int dx = x + 19 + textWidth(&QuicksandMedium13, label, strlen(label)) + 4;
    for (int i = 0; i < 3; i++) bar->fillCircle(dx + i * 6, 11, i == st.spin % 3 ? 2 : 1, th->accent);
  }
  if (st.rec) {
    if (st.dot) bar->fillRect(262, 7, 8, 8, th->ink);
    else        bar->drawRect(262, 7, 8, 8, th->dim);
    bar->setTextColor(th->ink);
    bar->setCursor(274, 8);
    bar->print("REC");
  } else if (st.fps[0]) {
    bar->setTextColor(th->dim);
    bar->setCursor(262, 8);
    bar->print(st.fps);
  }
  tft.drawRGBBitmap(0, 0, bar->getBuffer(), 320, 22);
  shown = st;
  barDirty = false;
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
  barDirty = true;

  body = new GFXcanvas16(BODY_W, BODY_H);
  stage = new GFXcanvas16(320, STAGE_H);
  term = new GFXcanvas16(320, TERM_H);
  bar = new GFXcanvas16(320, 22);
  if (!body->getBuffer() || !stage->getBuffer() || !term->getBuffer() || !bar->getBuffer()) Serial.println("canvas alloc FAILED");
  for (int i = 0; i < 18; i++) rain[i] = {4 + i * 18, (float)random(0, 200), 1.5f + random(0, 25) / 10.0f};
}

// theme switch: repaint everything in the new colours
static void applyTheme() {
  const Theme *want = hackerMode ? &HACKER : &NORMAL;
  if (want != th) {
    th = want;
    tft.fillScreen(th->bg);
    barDirty = true;
  }
}

// frame rate while an app runs, in the top bar: shows what a faster display link buys
static void drawFps(uint32_t now) {
  static uint32_t frames = 0, since = 0;
  frames++;
  if (now - since < 500) return;
  snprintf(fpsText, sizeof fpsText, "%2lu fps", (unsigned long)(frames * 1000 / (now - since)));
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
  drawBar(now);
}

void faceSetLink(bool up) { linkUp = up; }

void faceSetSnap(const uint16_t *pixels, uint32_t since) {
  snapPixels = pixels;
  snapAt = since;
}

void faceSetClaude(uint32_t now, bool on) {
  if (on == claudeOn) return;
  claudeOn = on;
  if (on) {
    claudeSince = now;
    claudeUntil = 0;
  } else {
    claudeUntil = now;
  }
}

void faceSetFo(FoState state, uint32_t calledAt, uint32_t since, FoTask task, int out) {
  foOut = out;
  foState = state;
  foCalledAt = calledAt;
  foSince = since;
  foTask = task;
}

void faceFrame(uint32_t now, Face f, const Words &words) {
  applyTheme();
  if (shownFps) {   // an app just ended: clear its frame-rate readout
    fpsText[0] = 0;
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
  drawBar(now);
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
  drawBar(now);
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
  barDirty = true;
  shownFps = false;
}
