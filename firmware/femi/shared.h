#pragma once
#include "config.h"

// The mailbox between the speech task (writes) and loop() (reads).
// Each value is a single word, which this chip reads and writes atomically.
// Defined in buddy.ino.
extern volatile Face wanted;          // mood the face should show
extern volatile bool listening;       // speech engine is waiting for a command
extern volatile bool hackerMode;      // green theme
extern volatile uint32_t moodUntil;   // when a timed mood (happy, thinking) ends
extern volatile int timerReq;         // pending timer request (REQ_* or minutes)
extern volatile Ask asking;           // open question a spoken number would answer
extern volatile int appReq;           // APP_* to start, APP_QUIT to leave, APP_NONE for nothing
extern volatile bool approvalOpen;    // an approval card is up: "yes" / "no" are answers
extern volatile int voiceAnswer;      // a spoken answer to it: 1 yes, 0 no, -1 none yet
extern volatile bool remindOpen;      // a reminder card is up: "done" acknowledges it (voiceAnswer = 1)
extern volatile bool talkOpen;        // Famyy is working or talking: "Famyy stop" or "hey Famyy" can interrupt it
extern volatile int bargeIn;          // what was said to interrupt: 1 stop, 2 stop and listen, 0 nothing
