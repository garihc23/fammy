#pragma once
#include <Adafruit_GFX.h>
#include "config.h"

// Famyy's body, drawn into any canvas at any spot: on its own, or beside Fo.
// Famyy is a little house with a face: the family's home, listening and helping.

struct Palette {
  uint16_t bg, ink, dim, soft;        // background, line art, faded, very faded (smoke, the floor)
  uint16_t wall, roof, door, blush;   // Famyy's colours
  uint16_t good, accent;              // a green tick; small highlights
  uint16_t fo, foInk;                 // Fo: white body, black lines
  uint16_t tag, tagFill, tagEdge;     // the name chip under each character: words, inside, outline
};

// how Famyy looks this frame
struct Look {
  int cx;              // centre of the house, canvas x
  Face face;
  float blink;         // 1 open .. 0 shut
  float gazeX, gazeY;  // where the eyes look, in pixels
  int talk;            // loudness of its own voice: opens the mouth whatever the face
  bool listen;         // sound waves coming in at its sides
  float reach;         // 0..1: right arm out, for a handshake or to catch something
  int handDy;          // the hand bobbing while it shakes
  const GFXfont *nameFont;   // its name chip at its feet, in this font (nullptr: no chip)
};

const int GROUND_Y = 192;                    // where feet stand: low on the stage, leaving the sky for words
                                             // and the floor below for name chips
const int CREATURE_HAND_Y = GROUND_Y - 47;   // hand height at rest (the middle of the walls)
const int OUTLINE = 3;             // Famyy's line weight

void drawFamyy(GFXcanvas16 *c, uint32_t t, const Look &k, const Palette &p);
int famyyHandX(const Look &k);   // where the reaching hand is
void famyyRoofTop(const Look &k, uint32_t t, int *x, int *y);   // the peak of the roof, where Claude sits
// a name chip on the floor under a character, like the name under someone in a video call
void nameChip(GFXcanvas16 *c, int cx, const char *name, const GFXfont *font, const Palette &p);

// drawing helpers, shared with Fo
void thickLine(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, uint16_t col);
void plusSign(GFXcanvas16 *c, int x, int y, int s, uint16_t col);   // a pixel sparkle
void fillEllipse(GFXcanvas16 *c, int cx, int cy, int rx, int ry, uint16_t col);
void arc(GFXcanvas16 *c, int cx, int cy, int r, float from, float to, int thick, uint16_t col);   // angles in half-turns (0..2)
void limb(GFXcanvas16 *c, int x0, int y0, int x1, int y1, int w, int out, uint16_t fill, uint16_t ink);   // outlined arm or leg, round end
