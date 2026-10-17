#include "app.h"
#include "settings.h"

// Morse teacher, basic level: a letter appears, you key it on the touch pad.
// Short press = dot, long press = dash. It checks as you go: right the moment the pattern matches,
// wrong the moment a symbol doesn't. New letters unlock as you get them right.
static const int W = 220;
static const char ORDER[] = "ETIANMSURWDKGOHVFLPJBXCYZQ";   // shortest codes first
static const char *CODES[26] = {
  ".-", "-...", "-.-.", "-..", ".", "..-.", "--.", "....", "..", ".---", "-.-", ".-..", "--",
  "-.", "---", ".--.", "--.-", ".-.", "...", "-", "..-", "...-", ".--", "-..-", "-.--", "--.."
};
static const uint32_t STALL_MS = 1500;   // stopped halfway through a letter this long: counts as a miss
static const int STREAK_TO_UNLOCK = 5;   // right answers in a row to earn the next letter
static const int HINT_UNTIL = 2;         // show the pattern until a letter has been right this many times
static const int MAX_KEYS = 6;
static const uint32_t TAPE_MS = 3000;    // how much history the telegraph tape shows
static const int TAPE_CELLS = 100;

enum Phase { ASK, RIGHT, WRONG };
static Phase phase;
static int unlocked;          // how many letters of ORDER are in play (saved)
static char target, prevTarget;
static char input[MAX_KEYS + 1];
static int inLen, streak;
static int rightCount[26];    // right answers per letter this session
static bool showHint, justUnlocked, keyDown;
static uint32_t keyHeld, phaseSince, lastKey, lastActive;
static float avgDot = 140;    // ms: learned from your own keying, so the dot/dash split fits your rhythm
static bool tape[TAPE_CELLS];  // key up/down history, one cell per TAPE_MS / TAPE_CELLS
static uint32_t tapeCell;

static const char *codeOf(char c) { return CODES[c - 'A']; }
static uint32_t dashFrom() { return constrain((uint32_t)(avgDot * 2.2f), 220u, 480u); }   // a dash is ~3 dots long

static void nextLetter(uint32_t now) {
  char t;
  if (justUnlocked) {
    t = ORDER[unlocked - 1];   // introduce the new letter straight away
  } else {
    do {   // half the time drill the newest letter, otherwise anything unlocked
      t = random(2) ? ORDER[unlocked - 1] : ORDER[random(unlocked)];
    } while (t == prevTarget);
  }
  prevTarget = target = t;
  showHint = rightCount[t - 'A'] < HINT_UNTIL;
  inLen = 0;
  input[0] = 0;
  phase = ASK;
  phaseSince = now;
}

static void finish(bool ok, uint32_t now) {
  phase = ok ? RIGHT : WRONG;
  phaseSince = now;
  justUnlocked = false;
  if (!ok) {
    streak = 0;
    return;
  }
  rightCount[target - 'A']++;
  if (++streak >= STREAK_TO_UNLOCK && unlocked < 26) {
    unlocked++;
    streak = 0;
    justUnlocked = true;
    settingsPut("morse", unlocked);
  }
}

static void key(uint32_t ms, uint32_t now) {
  bool dot = ms < dashFrom();
  if (dot) avgDot = constrain(avgDot + (ms - avgDot) * 0.2f, 60.0f, 220.0f);
  input[inLen++] = dot ? '.' : '-';
  input[inLen] = 0;
  lastKey = now;
  const char *want = codeOf(target);
  if (strncmp(input, want, inLen) != 0) finish(false, now);   // a wrong symbol: say so straight away
  else if (want[inLen] == 0) finish(true, now);               // the whole pattern, right
}

static void recordTape(uint32_t now, bool down, bool pressedSinceLast) {
  uint32_t cell = now / (TAPE_MS / TAPE_CELLS);
  if (cell - tapeCell > TAPE_CELLS) tapeCell = cell - TAPE_CELLS;
  while (tapeCell < cell) {
    tapeCell++;
    tape[tapeCell % TAPE_CELLS] = down || pressedSinceLast;   // a tap shorter than a frame still leaves a mark
    pressedSinceLast = false;
  }
}

static void start(uint32_t now) {
  unlocked = constrain((int)settingsGet("morse", 2), 2, 26);
  memset(rightCount, 0, sizeof rightCount);
  memset(tape, 0, sizeof tape);
  tapeCell = now / (TAPE_MS / TAPE_CELLS);
  streak = 0;
  prevTarget = 0;
  justUnlocked = false;
  lastActive = now;
  nextLetter(now);
}

static bool update(uint32_t now, const TouchInput &t) {
  keyDown = t.down;
  keyHeld = t.heldMs;
  if (t.down || t.releases) lastActive = now;
  recordTape(now, t.down, t.releases > 0);

  switch (phase) {
    case ASK:
      for (int i = 0; i < t.releases && phase == ASK; i++) key(t.releasedMs[i], now);
      if (phase == ASK && inLen > 0 && !t.down && now - lastKey > STALL_MS) finish(false, now);
      break;
    case RIGHT:
      if (now - phaseSince > (justUnlocked ? 2200 : 650)) nextLetter(now);
      break;
    case WRONG:
      if (now - phaseSince > 1400) {   // same letter again, this time with the pattern shown
        inLen = 0;
        input[0] = 0;
        showHint = true;
        phase = ASK;
        phaseSince = now;
      }
      break;
  }
  return now - lastActive < 60000;   // a minute untouched: back to the buddy
}

// dots and dashes as shapes, centred on row y; the last one can be drawn in its own colour
static void drawPattern(GFXcanvas16 *c, const char *p, int y, uint16_t col, uint16_t lastCol) {
  int n = strlen(p), w = 0;
  for (int i = 0; i < n; i++) w += (p[i] == '.' ? 10 : 24) + (i ? 8 : 0);
  int x = (W - w) / 2;
  for (int i = 0; i < n; i++) {
    uint16_t cc = i == n - 1 ? lastCol : col;
    if (i) x += 8;
    if (p[i] == '.') {
      c->fillCircle(x + 5, y, 5, cc);
      x += 10;
    } else {
      c->fillRoundRect(x, y - 4, 24, 9, 4, cc);
      x += 24;
    }
  }
}

static void draw(GFXcanvas16 *c, const Ink &k) {
  uint32_t now = millis();
  c->fillScreen(k.bg);
  uint32_t age = now - phaseSince;

  // the letter: green and bouncing when right, red and shaking when wrong
  char letter[2] = {target, 0};
  int ly = 4, lx = 0;
  uint16_t lc = k.ink;
  if (phase == RIGHT) {
    lc = k.good;
    ly -= (int)(sinf(min(age, (uint32_t)300) / 300.0f * PI) * 6);
  } else if (phase == WRONG) {
    lc = k.bad;
    if (age < 300) lx = ((now / 40) % 2) ? 4 : -4;
  }
  c->setTextColor(lc);
  c->setTextSize(6);
  c->setCursor((W - 36) / 2 + lx, ly);
  c->print(letter);
  if (phase == RIGHT && justUnlocked) {   // a new letter: sparkles all round
    for (int i = 0; i < 6; i++) {
      float a = i * PI / 3 + now / 300.0f;
      drawSparkle(c, W / 2 + (int)(cosf(a) * 44), 28 + (int)(sinf(a) * 26), 2 + (int)((now / 90 + i) % 4), i % 2 ? k.warn : k.good);
    }
  }

  // streak: one box per right answer towards the next letter
  for (int i = 0; i < STREAK_TO_UNLOCK; i++) {
    int bx = W - 8 - (STREAK_TO_UNLOCK - i) * 9;
    if (i < streak) c->fillRect(bx, 6, 7, 7, k.good);
    else            c->drawRect(bx, 6, 7, 7, k.dim);
  }

  // the pattern to copy: for new letters, after a mistake, and with the answer
  if (showHint || phase != ASK) drawPattern(c, codeOf(target), 66, k.dim, k.dim);

  // what you've keyed, plus the press in progress: it grows from dot to dash as you hold
  char shown[MAX_KEYS + 2];
  strcpy(shown, input);
  bool live = phase == ASK && keyDown && inLen < MAX_KEYS;
  if (live) {
    shown[inLen] = keyHeld < dashFrom() ? '.' : '-';
    shown[inLen + 1] = 0;
  }
  uint16_t doneCol = phase == WRONG ? k.bad : (phase == RIGHT ? k.good : k.ink);
  drawPattern(c, shown, 88, doneCol, live ? k.warn : doneCol);

  if (phase == RIGHT) {
    c->setTextColor(k.good);
    drawCentered(c, justUnlocked ? "new letter!" : "correct", 2, 102);
  } else if (phase == WRONG) {
    c->setTextColor(k.bad);
    drawCentered(c, "try again", 2, 102);
  } else if (inLen == 0 && !keyDown) {
    c->setTextColor(k.dim);
    drawCentered(c, "tap = dot   hold = dash", 1, 106);
  }

  // telegraph tape: your key presses over the last few seconds, newest on the right
  const int TX = 10, TW = W - 20, HI = 126, LO = 136;
  for (int i = 0; i < TAPE_CELLS; i++) {
    bool on = tape[(tapeCell + 1 + i) % TAPE_CELLS];
    int x0 = TX + i * TW / TAPE_CELLS, x1 = TX + (i + 1) * TW / TAPE_CELLS;
    c->drawFastHLine(x0, on ? HI : LO, x1 - x0, on ? k.accent : k.far);
    bool prev = tape[(tapeCell + i) % TAPE_CELLS];
    if (i > 0 && on != prev) c->drawFastVLine(x0, HI, LO - HI, k.accent);
  }

  // letters learned so far, the newest highlighted
  int rowX = (W - unlocked * 8) / 2;
  c->setTextSize(1);
  for (int i = 0; i < unlocked; i++) {
    c->setTextColor(i == unlocked - 1 ? k.accent : (ORDER[i] == target ? k.ink : k.dim));
    c->setCursor(rowX + i * 8, 156);
    c->print(ORDER[i]);
  }
}

static void status(char *buf, size_t len) {
  snprintf(buf, len, "morse  %d/26  streak %d", unlocked, streak);
}

const App MORSE_APP = {start, update, draw, status};
