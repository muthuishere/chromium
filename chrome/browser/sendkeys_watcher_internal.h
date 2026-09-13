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

// ---------------------------------------------------------------------------
// Verb parsing for the agent-protocol verbs (VERSION / COOKIEEXPORT /
// COOKIEIMPORT). These are pure string functions so the dispatch contract can
// be pinned by unit tests without standing up a browser or a cookie store.
// ---------------------------------------------------------------------------

// The protocol version this engine speaks. Bumped only on a breaking change to
// the spool wire format. Mirrors `Protocol` in the Go client (ADR 0009 SS6).
inline constexpr int kAgentProtocolVersion = 1;

// Fork-local engine version, independent of the Chromium milestone underneath.
inline constexpr char kAgentEngineVersion[] = "0.3.0";

// Returns the results/<id> this line will ack on, or "" if the line is
// fire-and-forget. This is load-bearing, not cosmetic: it is what lets a
// command that cannot run (no tab, unknown tabId) write an error ack instead of
// vanishing, and a vanished ack is a client that hangs forever.
std::string MaybeResultIdFor(const std::string& line);

// Result of parsing an "<id>|<payload>" verb body. `error` is set (and `ok`
// false) when the payload is missing or empty -- e.g. a bare COOKIEEXPORT with
// no domain, which ADR 0011 SS2 makes a refusal rather than a convenience.
struct ParsedIdArg {
  bool ok = false;
  std::string id;
  std::string arg;
  std::string error;
};

// COOKIEEXPORT:<id>|<domain>. A missing or empty <domain> is refused: exporting
// the whole jar is an all-identities-at-once artifact and must be asked for
// explicitly, never produced by leaving an argument off.
ParsedIdArg ParseCookieExport(const std::string& spec);

// COOKIEIMPORT:<id>|<json>. The payload is JSON and may itself contain '|', so
// only the FIRST separator is consumed.
ParsedIdArg ParseCookieImport(const std::string& spec);

// True when a cookie whose Domain attribute is `cookie_domain` belongs to the
// export scope `requested`. Host-only cookies match exactly; a leading-dot
// (domain) cookie matches the domain and every subdomain. Matching is on label
// boundaries, so "evil-linkedin.com" is NOT in scope for "linkedin.com".
bool CookieDomainInScope(const std::string& cookie_domain,
                         const std::string& requested);

// Returns |line| safe to write to a log: the payload of a COOKIEIMPORT /
// COOKIEEXPORT line (anything after "<verb><id>|") is replaced by
// "<redacted>". ADR 0011: the engine writes no cookie value into any log.
std::string RedactLineForLog(const std::string& line);


}  // namespace sendkeys::internal

#endif  // CHROME_BROWSER_SENDKEYS_WATCHER_INTERNAL_H_
