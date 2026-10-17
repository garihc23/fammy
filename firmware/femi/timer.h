#pragma once
#include <Arduino.h>

enum TimerPhase { T_OFF, T_FOCUS, T_BREAK, T_DONE };

// apply one request (REQ_* or minutes, REQ_NONE for nothing), then advance the countdown
void timerUpdate(uint32_t now, int request);

TimerPhase timerPhase();
bool timerPaused();
bool timerCounting();             // focus or break in progress, paused or not
uint32_t timerLeft(uint32_t now); // ms
uint32_t timerTotal();            // ms, length of the current phase
