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

// ---------------------------------------------------------------------------
// The agent-protocol verbs (VERSION / COOKIEEXPORT / COOKIEIMPORT).
// ---------------------------------------------------------------------------

// Every acking verb must be recognised here. This is not bookkeeping: the id
// this returns is what lets a command that cannot run write an error ack, and a
// verb missing from this list fails by HANGING the client forever on a result
// file nobody will write -- the exact failure the VERSION verb exists to kill.
TEST(SendKeysResultIdTest, AckingVerbsAreAllRecognised) {
  EXPECT_EQ(MaybeResultIdFor("VERSION:v1"), "v1");
  EXPECT_EQ(MaybeResultIdFor("EVAL:e1|document.title"), "e1");
  EXPECT_EQ(MaybeResultIdFor("EVALASYNC:a1|return 1"), "a1");
  EXPECT_EQ(MaybeResultIdFor("LISTTABS:l1"), "l1");
  EXPECT_EQ(MaybeResultIdFor("WAITFOR:5000|w1|x"), "w1");
  EXPECT_EQ(MaybeResultIdFor("NEWTAB:n1|https://x.com"), "n1");
  EXPECT_EQ(MaybeResultIdFor("SCREENSHOT:s1|/tmp/a.png"), "s1");
  EXPECT_EQ(MaybeResultIdFor("COOKIEEXPORT:c1|linkedin.com"), "c1");
  EXPECT_EQ(MaybeResultIdFor("COOKIEIMPORT:c2|[]"), "c2");
}

// The fire-and-forget forms have nothing to hang on, and must not invent an id
// out of their argument -- "/tmp/a.png" is not a result id.
TEST(SendKeysResultIdTest, FireAndForgetFormsHaveNoId) {
  EXPECT_EQ(MaybeResultIdFor("SCREENSHOT:/tmp/a.png"), "");
  EXPECT_EQ(MaybeResultIdFor("NEWTAB:https://x.com"), "");
  EXPECT_EQ(MaybeResultIdFor("TEXT:hello"), "");
  EXPECT_EQ(MaybeResultIdFor("KEY:enter"), "");
  EXPECT_EQ(MaybeResultIdFor("GOTO:https://x.com"), "");
  EXPECT_EQ(MaybeResultIdFor("AUDIOSTOP"), "");
  EXPECT_EQ(MaybeResultIdFor(""), "");
}

// ADR 0011 SS2: a bare export is a REFUSAL, not a convenience. The whole jar is
// every identity at once, so it must never be what you get by leaving an
// argument off.
TEST(SendKeysCookieExportTest, BareExportIsRefused) {
  ParsedIdArg no_arg = ParseCookieExport("c1");
  EXPECT_FALSE(no_arg.ok);
  EXPECT_EQ(no_arg.id, "c1");  // id survives, or the refusal cannot be delivered
  EXPECT_FALSE(no_arg.error.empty());

  ParsedIdArg empty = ParseCookieExport("c1|");
  EXPECT_FALSE(empty.ok);
  EXPECT_EQ(empty.id, "c1");

  ParsedIdArg blank = ParseCookieExport("c1|   ");
  EXPECT_FALSE(blank.ok);
  EXPECT_EQ(blank.id, "c1");
}

// A refusal with no id is undeliverable; the parse still has to say so rather
// than pretend it succeeded.
TEST(SendKeysCookieExportTest, MissingIdIsNotOk) {
  EXPECT_FALSE(ParseCookieExport("").ok);
  EXPECT_FALSE(ParseCookieExport("|linkedin.com").ok);
  EXPECT_TRUE(ParseCookieExport("|linkedin.com").id.empty());
}

// The domain is normalised, so a caller may paste a URL or a Set-Cookie-style
// leading-dot domain without getting an empty export and no explanation.
TEST(SendKeysCookieExportTest, DomainIsNormalised) {
  EXPECT_EQ(ParseCookieExport("c|linkedin.com").arg, "linkedin.com");
  EXPECT_EQ(ParseCookieExport("c|.linkedin.com").arg, "linkedin.com");
  EXPECT_EQ(ParseCookieExport("c|LinkedIn.COM").arg, "linkedin.com");
  EXPECT_EQ(ParseCookieExport("c|https://www.linkedin.com/feed/").arg,
            "www.linkedin.com");
  EXPECT_EQ(ParseCookieExport("c|http://x.com").arg, "x.com");
  EXPECT_TRUE(ParseCookieExport("c|linkedin.com").ok);
  EXPECT_FALSE(ParseCookieExport("c|https://").ok);
}

// Scope is decided on LABEL boundaries. Without that, "evil-linkedin.com"
// exports as part of "linkedin.com" -- a plain suffix check hands a hostile
// registration somebody's session.
TEST(SendKeysCookieScopeTest, LabelBoundaryOnly) {
  EXPECT_TRUE(CookieDomainInScope("linkedin.com", "linkedin.com"));
  EXPECT_TRUE(CookieDomainInScope(".linkedin.com", "linkedin.com"));
  EXPECT_TRUE(CookieDomainInScope("www.linkedin.com", "linkedin.com"));
  EXPECT_TRUE(CookieDomainInScope(".www.linkedin.com", ".linkedin.com"));
  EXPECT_TRUE(CookieDomainInScope("LINKEDIN.com", "linkedin.com"));

  EXPECT_FALSE(CookieDomainInScope("evil-linkedin.com", "linkedin.com"));
  EXPECT_FALSE(CookieDomainInScope("linkedin.com.evil.test", "linkedin.com"));
  EXPECT_FALSE(CookieDomainInScope("x.com", "linkedin.com"));
  // A narrower request must not drag in the parent domain's cookies.
  EXPECT_FALSE(CookieDomainInScope("linkedin.com", "www.linkedin.com"));
  EXPECT_FALSE(CookieDomainInScope("", "linkedin.com"));
  EXPECT_FALSE(CookieDomainInScope("linkedin.com", ""));
}

// The import payload is JSON and will contain '|' (in a value, in a URL). Only
// the FIRST separator may be consumed or the jar arrives truncated.
TEST(SendKeysCookieImportTest, SplitsOnFirstSeparatorOnly) {
  ParsedIdArg p = ParseCookieImport(R"(c1|[{"name":"a","value":"x|y|z"}])");
  EXPECT_TRUE(p.ok);
  EXPECT_EQ(p.id, "c1");
  EXPECT_EQ(p.arg, R"([{"name":"a","value":"x|y|z"}])");
}

TEST(SendKeysCookieImportTest, EmptyPayloadIsRefusedWithADeliverableId) {
  ParsedIdArg p = ParseCookieImport("c1");
  EXPECT_FALSE(p.ok);
  EXPECT_EQ(p.id, "c1");
  EXPECT_FALSE(p.error.empty());
}

// ADR 0011: no cookie value in any log. The early-failure paths (no tab, bad
// TAB: prefix) log the raw line, and for COOKIEIMPORT the raw line IS the jar.
TEST(SendKeysRedactTest, CookiePayloadNeverReachesTheLog) {
  const std::string line =
      R"(COOKIEIMPORT:c1|[{"name":"li_at","value":"SECRET-SESSION"}])";
  std::string redacted = RedactLineForLog(line);
  EXPECT_EQ(redacted.find("SECRET-SESSION"), std::string::npos);
  EXPECT_EQ(redacted, "COOKIEIMPORT:c1|<redacted>");

  // Behind a TAB: prefix too.
  std::string tabbed = RedactLineForLog(
      R"(TAB:abc|COOKIEIMPORT:c1|[{"value":"SECRET-SESSION"}])");
  EXPECT_EQ(tabbed.find("SECRET-SESSION"), std::string::npos);

  // Non-cookie lines are untouched.
  EXPECT_EQ(RedactLineForLog("EVAL:e1|1+1"), "EVAL:e1|1+1");
}

// The handshake's own numbers. A client pins these (ADR 0009 SS6), so a change
// here is a protocol change and should have to be made twice, on purpose.
TEST(SendKeysVersionTest, ProtocolConstants) {
  EXPECT_EQ(kAgentProtocolVersion, 1);
  EXPECT_STRNE(kAgentEngineVersion, "");
}

}  // namespace
}  // namespace sendkeys::internal
