#pragma once
#include "config.h"
#include "app.h"

void faceBegin();                       // set up the display and drawing buffers
void faceFrame(uint32_t now, Face f);   // animate and draw one frame showing face f
void faceAppFrame(uint32_t now, const App &app, float exitHold);   // draw an app instead; exitHold 0..1 shows the hold-to-exit bar
