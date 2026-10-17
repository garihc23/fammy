#pragma once

// start the mic and the speech engine (wake word "Hi ESP" + voice commands).
// Recognised commands are posted to the mailbox in shared.h.
void speechBegin();
void speechPause();   // stop listening for the wake phrase (while Femi records, thinks and talks)
void speechRearm();   // listen for the wake phrase again
