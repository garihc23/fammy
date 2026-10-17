#include "timer.h"
#include "config.h"

// timer state: `static` keeps it private to this file, like a private field
static TimerPhase phase = T_OFF, doneFrom = T_OFF;
static uint32_t endAt = 0, total = 0, remainWhilePaused = 0, doneSince = 0;
static bool paused = false, pomodoro = false;

TimerPhase timerPhase() { return phase; }
bool timerPaused() { return paused; }
bool timerCounting() { return phase == T_FOCUS || phase == T_BREAK; }
uint32_t timerTotal() { return total; }

uint32_t timerLeft(uint32_t now) {
  if (paused) return remainWhilePaused;
  return (int32_t)(endAt - now) > 0 ? endAt - now : 0;
}

static void start(uint32_t now, uint32_t ms, TimerPhase p, bool pomo) {
  phase = p;
  total = ms;
  endAt = now + ms;
  paused = false;
  pomodoro = pomo;
}

static void endAlarm(uint32_t now) {
  if (pomodoro && doneFrom == T_FOCUS) start(now, BREAK_MIN * 60000UL, T_BREAK, true);   // focus rolls into break
  else phase = T_OFF;
}

void timerUpdate(uint32_t now, int req) {
  if (req > 0)                  start(now, req * 60000UL, T_FOCUS, false);
  else if (req == REQ_POMODORO) start(now, FOCUS_MIN * 60000UL, T_FOCUS, true);
  else if (req == REQ_CANCEL)   phase = T_OFF;
  else if (req == REQ_PAUSE && !paused && timerCounting()) {
    remainWhilePaused = timerLeft(now);
    paused = true;
  } else if (req == REQ_RESUME && paused) {
    endAt = now + remainWhilePaused;
    paused = false;
  } else if (req == REQ_DISMISS && phase == T_DONE) endAlarm(now);

  if (timerCounting() && !paused && timerLeft(now) == 0) {
    doneFrom = phase;
    phase = T_DONE;
    doneSince = now;
  }
  // stop flashing on its own: a pomodoro moves on to its break after 10 s, a plain timer gives up after a minute
  if (phase == T_DONE && now - doneSince > (pomodoro && doneFrom == T_FOCUS ? 10000UL : 60000UL)) endAlarm(now);
}
