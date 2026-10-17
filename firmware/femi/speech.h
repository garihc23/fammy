#pragma once

// start the mic and the speech engine (wake word "Hi ESP" + voice commands).
// Recognised commands are posted to the mailbox in shared.h.
void speechBegin();
