// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Pure, dependency-light parsing helpers for the sendkeys spike, split out of
// sendkeys_watcher.cc so they can be unit-tested (see
// sendkeys_watcher_unittest.cc) without standing up a browser. Everything
// here is a free function over strings -- no RenderWidgetHost, no threads, no
// file I/O.

#ifndef CHROME_BROWSER_SENDKEYS_WATCHER_INTERNAL_H_
#define CHROME_BROWSER_SENDKEYS_WATCHER_INTERNAL_H_

#include <string>

#include "ui/events/keycodes/dom/dom_code.h"
#include "ui/events/keycodes/dom/dom_key.h"
#include "ui/events/keycodes/keyboard_codes.h"

namespace sendkeys::internal {

// The parsed form of a KEY: trigger like "ctrl+shift+t", "enter", or "a".
// `ok` is false if the trigger could not be parsed.
struct ParsedTrigger {
  bool ok = false;
  int modifiers = 0;  // blink::WebInputEvent modifier bits.
  ui::DomKey dom_key;
  ui::DomCode dom_code = ui::DomCode::NONE;
  ui::KeyboardCode key_code = ui::VKEY_UNKNOWN;
};

// Parses a KEY: trigger: zero or more '+'-separated modifiers
// (ctrl/control, shift, alt/option, cmd/command/meta/super) followed by one
// key token, which is either a named key (enter, tab, escape, space,
// backspace, delete, up/down/left/right) or a single US-layout character.
ParsedTrigger ParseTrigger(const std::string& trigger);

}  // namespace sendkeys::internal

#endif  // CHROME_BROWSER_SENDKEYS_WATCHER_INTERNAL_H_
