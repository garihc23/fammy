#pragma once
#include <Arduino.h>

// Settings that survive a power-off, kept in the chip's flash (NVS: a small key-value store).
void settingsBegin();    // load saved values; call before anything uses them
void settingsUpdate();   // save anything that changed; call from loop(), never from the speech task

// small numbers apps want to keep, like a best score or how far you got
uint32_t settingsGet(const char *key, uint32_t fallback);
void settingsPut(const char *key, uint32_t value);   // loop() only: writes flash
