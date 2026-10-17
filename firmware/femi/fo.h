#pragma once
#include "creature.h"

// Fo, Wajo's errand agent, as a character on Famyy's stage. When Famyy hands a job to Fo:
// Famyy steps aside, Fo walks in from the right, they shake hands, Fo works (an icon shows
// what kind of job), then Fo tosses the result to Famyy, waves, and walks off.
// Everything here is a pure function of time, so the preview tool draws exactly what the device does.

enum FoState : uint8_t { FO_OFF, FO_WORKING, FO_DONE, FO_FAILED };
enum FoTask : uint8_t { TASK_WEB, TASK_CALL, TASK_MESSAGE, TASK_EMAIL, TASK_SHOP, TASK_CALENDAR };
enum FoPhase : uint8_t { PH_NONE, PH_ENTER, PH_SHAKE, PH_WORK, PH_HANDOFF, PH_SHRUG, PH_LEAVE };

struct FoPose {
  FoPhase phase;
  uint32_t tp;                // ms into the phase
  int famyyX;                 // where Famyy stands
  float famyyReach;           // Famyy's arm out: handshake, or catching the result
  int handDy;                 // both hands bob together while shaking
  bool lookAtFo;              // Famyy's eyes follow Fo
  int foX;                    // where Fo stands (off-screen when it isn't there)
  float foReach;              // Fo's arm out towards Famyy
  bool foWalk, foWave, foShrug, foHappy;
  bool token;                 // the result flying from Fo to Famyy
  int tokenX, tokenY;
  int sparkle;                // 0 none, else the size of the sparkles around the hands
  int sparkleX, sparkleY;
};

const uint32_t FO_ENTER_MS = 1100, FO_SHAKE_MS = 800, FO_HANDOFF_MS = 1000, FO_LEAVE_MS = 1000;

FoTask foTaskFromName(const char *name);   // "call" "message" "email" "shop" "calendar", anything else is web
// the scene at time now: Fo was called at calledAt; state changed to done/failed at since.
// homeX is where Famyy stands when Fo isn't there (it moves aside to make room for a speech bubble).
FoPose foPose(uint32_t now, FoState state, uint32_t calledAt, uint32_t since, int homeX);
void foPlaceFamyy(const FoPose &p, Look &k);   // moves Famyy aside, turns its eyes to Fo, puts its hand out
void foDraw(GFXcanvas16 *c, uint32_t now, const FoPose &p, FoTask task, const Palette &pal);
