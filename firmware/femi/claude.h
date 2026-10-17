#pragma once
#include <Adafruit_GFX.h>

// Claude, Famyy's brain, as a character: Claude Code's little orange pixel mascot. While Famyy waits on
// Claude it drops onto Famyy's roof, thinks there with Claude's spark spinning over its head (and watches
// Fo when Fo comes), then hops for joy and pops away when the answer is ready.
// A pure function of time, like Fo's scene, so the preview tool draws what the device does.

struct ClaudePose {
  bool shown;
  int dy;            // above or below its seat on the roof (dropping in, hopping out)
  bool squash;       // just landed
  bool armsUp;       // the happy hop at the end
  int lookX, lookY;  // eyes, in pixels
  bool spark;        // Claude's spark spinning above its head
  int sparkle;       // popping away: size of the sparkle burst, 0 for none
};

const uint32_t CLAUDE_DROP_MS = 450, CLAUDE_LEAVE_MS = 500;

// on: Famyy is waiting on Claude, since then; until: when it stopped waiting (0 while still on).
// foHere turns its eyes towards Fo.
ClaudePose claudePose(uint32_t now, bool on, uint32_t since, uint32_t until, bool foHere);
// x, y: where its feet go (the top of Famyy's roof)
void drawClaude(GFXcanvas16 *c, uint32_t now, const ClaudePose &p, int x, int y, uint16_t body, uint16_t eyes);
