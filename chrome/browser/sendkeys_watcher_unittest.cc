// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "chrome/browser/sendkeys_watcher_internal.h"

#include "testing/gtest/include/gtest/gtest.h"
#include "third_party/blink/public/common/input/web_input_event.h"
#include "ui/events/keycodes/dom/dom_code.h"
#include "ui/events/keycodes/dom/dom_key.h"
#include "ui/events/keycodes/keyboard_codes.h"

namespace sendkeys::internal {
namespace {

// A plain single character maps to its US-layout key with no modifiers.
TEST(SendKeysParseTriggerTest, SingleCharacter) {
  ParsedTrigger t = ParseTrigger("a");
  EXPECT_TRUE(t.ok);
  EXPECT_EQ(t.modifiers, 0);
  EXPECT_EQ(t.key_code, ui::VKEY_A);
  EXPECT_EQ(t.dom_code, ui::DomCode::US_A);
}

// Named keys resolve to their dedicated keycodes.
TEST(SendKeysParseTriggerTest, NamedKeys) {
  EXPECT_EQ(ParseTrigger("enter").key_code, ui::VKEY_RETURN);
  EXPECT_EQ(ParseTrigger("return").key_code, ui::VKEY_RETURN);
  EXPECT_EQ(ParseTrigger("tab").key_code, ui::VKEY_TAB);
  EXPECT_EQ(ParseTrigger("escape").key_code, ui::VKEY_ESCAPE);
  EXPECT_EQ(ParseTrigger("esc").key_code, ui::VKEY_ESCAPE);
  EXPECT_EQ(ParseTrigger("space").key_code, ui::VKEY_SPACE);
  EXPECT_EQ(ParseTrigger("backspace").key_code, ui::VKEY_BACK);
  EXPECT_EQ(ParseTrigger("delete").key_code, ui::VKEY_DELETE);
  EXPECT_EQ(ParseTrigger("up").key_code, ui::VKEY_UP);
  EXPECT_EQ(ParseTrigger("down").key_code, ui::VKEY_DOWN);
  EXPECT_EQ(ParseTrigger("left").key_code, ui::VKEY_LEFT);
  EXPECT_EQ(ParseTrigger("right").key_code, ui::VKEY_RIGHT);
  for (const char* name :
       {"enter", "tab", "escape", "space", "up", "delete"}) {
    EXPECT_TRUE(ParseTrigger(name).ok) << name;
    EXPECT_EQ(ParseTrigger(name).modifiers, 0) << name;
  }
}

// Each modifier keyword sets the matching blink modifier bit; synonyms agree.
TEST(SendKeysParseTriggerTest, SingleModifiers) {
  EXPECT_EQ(ParseTrigger("ctrl+a").modifiers,
            blink::WebInputEvent::kControlKey);
  EXPECT_EQ(ParseTrigger("control+a").modifiers,
            blink::WebInputEvent::kControlKey);
  EXPECT_EQ(ParseTrigger("shift+a").modifiers,
            blink::WebInputEvent::kShiftKey);
  EXPECT_EQ(ParseTrigger("alt+a").modifiers, blink::WebInputEvent::kAltKey);
  EXPECT_EQ(ParseTrigger("option+a").modifiers,
            blink::WebInputEvent::kAltKey);
  EXPECT_EQ(ParseTrigger("cmd+a").modifiers, blink::WebInputEvent::kMetaKey);
  EXPECT_EQ(ParseTrigger("command+a").modifiers,
            blink::WebInputEvent::kMetaKey);
  EXPECT_EQ(ParseTrigger("meta+a").modifiers,
            blink::WebInputEvent::kMetaKey);
  EXPECT_EQ(ParseTrigger("super+a").modifiers,
            blink::WebInputEvent::kMetaKey);
}

// Modifiers combine (OR together) and the trailing token is still the key.
TEST(SendKeysParseTriggerTest, CombinedModifiers) {
  ParsedTrigger t = ParseTrigger("ctrl+shift+t");
  EXPECT_TRUE(t.ok);
  EXPECT_EQ(t.modifiers,
            blink::WebInputEvent::kControlKey | blink::WebInputEvent::kShiftKey);
  EXPECT_EQ(t.key_code, ui::VKEY_T);

  ParsedTrigger enter = ParseTrigger("cmd+shift+enter");
  EXPECT_TRUE(enter.ok);
  EXPECT_EQ(enter.modifiers,
            blink::WebInputEvent::kMetaKey | blink::WebInputEvent::kShiftKey);
  EXPECT_EQ(enter.key_code, ui::VKEY_RETURN);
}

// Parsing is case-insensitive for both modifiers and named keys.
TEST(SendKeysParseTriggerTest, CaseInsensitive) {
  ParsedTrigger t = ParseTrigger("CTRL+Shift+ENTER");
  EXPECT_TRUE(t.ok);
  EXPECT_EQ(t.modifiers,
            blink::WebInputEvent::kControlKey | blink::WebInputEvent::kShiftKey);
  EXPECT_EQ(t.key_code, ui::VKEY_RETURN);
}

// Bad input never claims success.
TEST(SendKeysParseTriggerTest, InvalidInput) {
  EXPECT_FALSE(ParseTrigger("").ok);
  EXPECT_FALSE(ParseTrigger("hyper+a").ok);   // unknown modifier
  EXPECT_FALSE(ParseTrigger("notakey").ok);   // unknown multi-char key
  EXPECT_FALSE(ParseTrigger("ctrl+").ok);      // modifier, no key
  EXPECT_FALSE(ParseTrigger("+").ok);
}

}  // namespace
}  // namespace sendkeys::internal
