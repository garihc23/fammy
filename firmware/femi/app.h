#pragma once
#include <Adafruit_GFX.h>
#include "touch.h"

// colours of the current theme, handed to whatever is drawing
struct Ink {
  uint16_t bg, ink, dim, far;         // background, main line colour, faded, very faded
  uint16_t good, bad, warn, accent;   // green-ish, red-ish, amber-ish, purple-ish (all green in hacking mode)
};

// A full-screen app that takes over the creature zone: the runner game, the morse teacher, ...
// Every app has the same shape, so loop() and the face don't care which one is running.
struct App {
  void (*start)(uint32_t now);
  bool (*update)(uint32_t now, const TouchInput &touch);   // false = finished, back to the buddy
  void (*draw)(GFXcanvas16 *c, const Ink &ink);            // into the 220x172 creature zone
  void (*status)(char *buf, size_t len);                   // one line for the terminal
};

extern const App RUNNER_APP;   // runner.cpp
extern const App MORSE_APP;    // morse.cpp

inline void drawCentered(GFXcanvas16 *c, const char *s, int size, int y) {
  c->setTextSize(size);
  c->setCursor((c->width() - (int)strlen(s) * 6 * size) / 2, y);
  c->print(s);
}

// a four-point twinkle
inline void drawSparkle(GFXcanvas16 *c, int x, int y, int s, uint16_t col) {
  if (s < 1) return;
  c->fillRect(x - s, y - 1, 2 * s, 2, col);
  c->fillRect(x - 1, y - s, 2, 2 * s, col);
}

// shift every row sideways by dx: a whole-picture shake
inline void shakeCanvas(GFXcanvas16 *c, int dx) {
  if (dx == 0) return;
  uint16_t *buf = c->getBuffer();
  int w = c->width(), n = abs(dx);
  for (int row = 0; row < c->height(); row++) {
    uint16_t *r = buf + row * w;
    if (dx > 0) { memmove(r + n, r, (w - n) * 2); memset(r, 0, n * 2); }
    else        { memmove(r, r + n, (w - n) * 2); memset(r + w - n, 0, n * 2); }
  }
}
