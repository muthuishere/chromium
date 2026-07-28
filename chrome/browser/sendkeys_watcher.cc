// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "chrome/browser/sendkeys_watcher.h"

#include <algorithm>
#include <optional>
#include <string_view>
#include <utility>
#include <vector>

#include "base/containers/span.h"
#include "base/base64.h"
#include "base/environment.h"
#include "base/files/file_enumerator.h"
#include "base/files/file_util.h"
#include "base/functional/bind.h"
#include "base/json/json_writer.h"
#include "base/logging.h"
#include "base/memory/raw_ptr.h"
#include "base/strings/string_number_conversions.h"
#include "base/strings/string_split.h"
#include "base/strings/string_util.h"
#include "base/strings/utf_string_conversions.h"
#include "base/supports_user_data.h"
#include "base/uuid.h"
#include "base/task/sequenced_task_runner.h"
#include "base/task/thread_pool.h"
#include "base/threading/platform_thread.h"
#include "base/time/time.h"
#include "base/values.h"
#include "base/functional/callback_helpers.h"
#include "chrome/browser/sendkeys_watcher_internal.h"
#include "chrome/browser/ui/browser_window/public/browser_window_interface.h"
#include "chrome/browser/ui/browser_window/public/browser_window_interface_iterator.h"
#include "chrome/browser/ui/navigator/browser_navigator.h"
#include "chrome/browser/ui/navigator/browser_navigator_params.h"
#include "chrome/browser/ui/tabs/tab_enums.h"
#include "chrome/browser/ui/tabs/tab_strip_model.h"
#include "components/input/native_web_keyboard_event.h"
#include "components/viz/common/frame_sinks/copy_output_result.h"
#include "components/tabs/public/tab_interface.h"
#include "content/public/browser/browser_thread.h"
#include "content/public/browser/global_request_id.h"
#include "content/public/browser/navigation_controller.h"
#include "content/public/browser/render_frame_host.h"
#include "content/public/browser/render_widget_host_observer.h"
#include "content/public/browser/render_widget_host_view.h"
#include "content/public/browser/web_contents.h"
#include "content/public/browser/web_contents_observer.h"
#include "content/public/common/isolated_world_ids.h"
#include "base/numerics/byte_conversions.h"
#include "media/audio/agent_audio_bridge.h"
#include "media/audio/wav_audio_handler.h"
#include "media/base/audio_bus.h"
#include "media/capture/video/agent_video_bridge.h"
#include "net/base/ip_endpoint.h"
#include "net/base/net_errors.h"
#include "net/log/net_log_source.h"
#include "net/server/http_server.h"
#include "net/server/http_server_request_info.h"
#include "net/socket/tcp_server_socket.h"
#include "net/traffic_annotation/network_traffic_annotation.h"
#include "third_party/blink/public/common/input/web_mouse_event.h"
#include "third_party/blink/public/mojom/loader/resource_load_info.mojom.h"
#include "ui/base/page_transition_types.h"
#include "ui/base/window_open_disposition.h"
#include "ui/events/keycodes/dom/dom_code.h"
#include "ui/events/keycodes/dom/dom_key.h"
#include "ui/events/keycodes/dom/keycode_converter.h"
#include "ui/events/keycodes/keyboard_code_conversion.h"
#include "ui/gfx/codec/png_codec.h"
#include "url/gurl.h"

// Fork-local input-injection spike, not upstream Chromium behavior.
// See //CHROMIUM_SENDKEYS_SPEC.md for the design writeup and protocol.

namespace sendkeys {

namespace {

constexpr char kSpoolEnvVar[] = "CHROMIUM_SENDKEYS_DIR";
constexpr base::TimeDelta kPollInterval = base::Milliseconds(25);

// Builds and forwards one keyboard event. Mirrors the field-filling pattern
// content::SimulateCharTyped()/SimulateKeyPressImpl() use in
// content/public/test/browser_test_utils.cc -- that helper is test-only and
// not linkable from chrome/browser, so this reimplements the same technique
// against the production content::RenderWidgetHost API.
void ForwardOneKeyEvent(content::RenderWidgetHost* rwh,
                         blink::WebInputEvent::Type type,
                         ui::DomKey dom_key,
                         ui::DomCode dom_code,
                         ui::KeyboardCode key_code,
                         int modifiers) {
  input::NativeWebKeyboardEvent event(type, modifiers,
                                       base::TimeTicks::Now());
  event.dom_key = dom_key;
  event.dom_code = static_cast<int>(dom_code);
  event.native_key_code =
      ui::KeycodeConverter::DomCodeToNativeKeycode(dom_code);
  event.windows_key_code = key_code;
  event.is_system_key = false;
  event.skip_if_unhandled = true;
  if (type == blink::WebInputEvent::Type::kChar ||
      type == blink::WebInputEvent::Type::kRawKeyDown) {
    if (dom_key.IsCharacter()) {
      event.text[0] = dom_key.ToCharacter();
      event.unmodified_text[0] = dom_key.ToCharacter();
    } else {
      event.text[0] = key_code;
      event.unmodified_text[0] = key_code;
    }
  }
  rwh->ForwardKeyboardEvent(event);
}

// One press+release (plus a shift bracket if the character needs it) for a
// single Unicode character. Same technique as
// content::SimulateCharTyped() -- ui::DomKey::FromCharacter() +
// UsLayoutDomKeyToDomCode() handles any BMP character on a US layout, not
// just ASCII, which is a step beyond the ASCII-only terminal spike.
void TypeOneCharacter(content::RenderWidgetHost* rwh, char16_t character) {
  ui::DomKey dom_key;
  ui::DomCode dom_code;
  ui::KeyboardCode key_code;

  if (character == u'\t') {
    dom_key = ui::DomKey::TAB;
    dom_code = ui::DomCode::TAB;
    key_code = ui::VKEY_TAB;
  } else if (character == u'\n' || character == u'\r') {
    dom_key = ui::DomKey::ENTER;
    dom_code = ui::DomCode::ENTER;
    key_code = ui::VKEY_RETURN;
  } else {
    dom_key = ui::DomKey::FromCharacter(character);
    dom_code = ui::UsLayoutDomKeyToDomCode(dom_key);
    key_code = ui::DomCodeToUsLayoutKeyboardCode(dom_code);
  }

  if (dom_code == ui::DomCode::NONE) {
    LOG(WARNING) << "sendkeys: no US-layout mapping for character "
                 << static_cast<int>(character) << ", skipping";
    return;
  }

  bool needs_shift =
      (character != ui::DomCodeToUsLayoutCharacter(dom_code, ui::EF_NONE));
  int modifiers = needs_shift ? blink::WebInputEvent::kShiftKey : 0;

  if (needs_shift) {
    ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kRawKeyDown,
                       ui::DomKey::SHIFT, ui::DomCode::SHIFT_LEFT,
                       ui::VKEY_SHIFT, modifiers);
  }
  ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kRawKeyDown, dom_key,
                     dom_code, key_code, modifiers);
  ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kChar, dom_key,
                     dom_code, key_code, modifiers);
  ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kKeyUp, dom_key,
                     dom_code, key_code, modifiers);
  if (needs_shift) {
    ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kKeyUp,
                       ui::DomKey::SHIFT, ui::DomCode::SHIFT_LEFT,
                       ui::VKEY_SHIFT, 0);
  }
}

}  // namespace

namespace internal {

// Minimal KEY: trigger parser -- "ctrl+shift+t", "cmd+c", "enter", "a".
//
// Unlike the terminal spike, this does NOT reuse an existing keybind parser:
// Chromium's own accelerators are built from ui::KeyboardCode + modifier
// bits in C++/resource tables, not parsed from a user-facing config string
// the way Ghostty's `keybind = ...` lines are, and no public runtime
// string->accelerator parser was found for reuse. This is new, dedicated,
// spike-only code.
ParsedTrigger ParseTrigger(const std::string& trigger) {
  ParsedTrigger result;
  std::vector<std::string> parts = base::SplitString(
      trigger, "+", base::TRIM_WHITESPACE, base::SPLIT_WANT_NONEMPTY);
  if (parts.empty()) {
    return result;
  }
  std::string key_token = base::ToLowerASCII(parts.back());
  parts.pop_back();
  for (const auto& mod : parts) {
    std::string m = base::ToLowerASCII(mod);
    if (m == "ctrl" || m == "control") {
      result.modifiers |= blink::WebInputEvent::kControlKey;
    } else if (m == "shift") {
      result.modifiers |= blink::WebInputEvent::kShiftKey;
    } else if (m == "alt" || m == "option") {
      result.modifiers |= blink::WebInputEvent::kAltKey;
    } else if (m == "cmd" || m == "command" || m == "meta" || m == "super") {
      result.modifiers |= blink::WebInputEvent::kMetaKey;
    } else {
      LOG(WARNING) << "sendkeys: unknown modifier '" << mod
                   << "' in trigger '" << trigger << "'";
      return result;
    }
  }

  struct NamedKey {
    std::string_view name;
    ui::DomKey dom_key;
    ui::DomCode dom_code;
    ui::KeyboardCode key_code;
  };
  static const NamedKey kNamed[] = {
      {"enter", ui::DomKey::ENTER, ui::DomCode::ENTER, ui::VKEY_RETURN},
      {"return", ui::DomKey::ENTER, ui::DomCode::ENTER, ui::VKEY_RETURN},
      {"tab", ui::DomKey::TAB, ui::DomCode::TAB, ui::VKEY_TAB},
      {"escape", ui::DomKey::ESCAPE, ui::DomCode::ESCAPE, ui::VKEY_ESCAPE},
      {"esc", ui::DomKey::ESCAPE, ui::DomCode::ESCAPE, ui::VKEY_ESCAPE},
      {"space", ui::DomKey::FromCharacter(' '), ui::DomCode::SPACE,
       ui::VKEY_SPACE},
      {"backspace", ui::DomKey::BACKSPACE, ui::DomCode::BACKSPACE,
       ui::VKEY_BACK},
      {"delete", ui::DomKey::DEL, ui::DomCode::DEL, ui::VKEY_DELETE},
      {"up", ui::DomKey::ARROW_UP, ui::DomCode::ARROW_UP, ui::VKEY_UP},
      {"down", ui::DomKey::ARROW_DOWN, ui::DomCode::ARROW_DOWN,
       ui::VKEY_DOWN},
      {"left", ui::DomKey::ARROW_LEFT, ui::DomCode::ARROW_LEFT,
       ui::VKEY_LEFT},
      {"right", ui::DomKey::ARROW_RIGHT, ui::DomCode::ARROW_RIGHT,
       ui::VKEY_RIGHT},
  };
  for (const auto& named : kNamed) {
    if (key_token == named.name) {
      result.dom_key = named.dom_key;
      result.dom_code = named.dom_code;
      result.key_code = named.key_code;
      result.ok = true;
      return result;
    }
  }
  if (key_token.size() == 1) {
    char16_t c = static_cast<char16_t>(key_token[0]);
    result.dom_key = ui::DomKey::FromCharacter(c);
    result.dom_code = ui::UsLayoutDomKeyToDomCode(result.dom_key);
    result.key_code = ui::DomCodeToUsLayoutKeyboardCode(result.dom_code);
    result.ok = (result.dom_code != ui::DomCode::NONE);
    return result;
  }
  LOG(WARNING) << "sendkeys: unrecognized key '" << key_token
               << "' in trigger '" << trigger << "'";
  return result;
}

}  // namespace internal

namespace {

blink::WebMouseEvent BuildMouseEvent(blink::WebInputEvent::Type type,
                                     blink::WebPointerProperties::Button button,
                                     int x,
                                     int y) {
  blink::WebMouseEvent event(type, gfx::PointF(x, y), gfx::PointF(x, y),
                             button, /*click_count_param=*/1,
                             blink::WebInputEvent::kNoModifiers,
                             base::TimeTicks::Now());
  event.button = button;
  event.click_count = 1;
  return event;
}

bool ParseXY(const std::string& text, int* x, int* y) {
  std::vector<std::string> parts = base::SplitString(
      text, ",", base::TRIM_WHITESPACE, base::SPLIT_WANT_NONEMPTY);
  if (parts.size() != 2) {
    return false;
  }
  return base::StringToInt(parts[0], x) && base::StringToInt(parts[1], y);
}

bool ConsumePrefix(const std::string& line,
                   std::string_view prefix,
                   std::string* rest) {
  if (line.size() < prefix.size() ||
      line.compare(0, prefix.size(), prefix) != 0) {
    return false;
  }
  *rest = line.substr(prefix.size());
  return true;
}

// Fork-local per-tab stable identity. A UUID minted lazily on first access and
// carried on the WebContents itself (base::SupportsUserData), so it travels with
// that exact tab for its whole life. Unlike a tab-strip index it never shifts
// when sibling tabs close; unlike a process-local counter it never collides
// across restarts -- a stale id simply resolves to no tab (see ResolveTabId).
const void* const kAgentTabIdKey = &kAgentTabIdKey;

class AgentTabIdData : public base::SupportsUserData::Data {
 public:
  const std::string id = base::Uuid::GenerateRandomV4().AsLowercaseString();
};

const std::string& GetOrCreateTabId(content::WebContents* wc) {
  auto* data = static_cast<AgentTabIdData*>(wc->GetUserData(kAgentTabIdKey));
  if (!data) {
    auto owned = std::make_unique<AgentTabIdData>();
    data = owned.get();
    wc->SetUserData(kAgentTabIdKey, std::move(owned));
  }
  return data->id;
}

// The results/<id>.json id the client will poll for a given inner command line,
// so an unknown-tabId failure can be reported instead of hanging the client on a
// result file that never appears. Empty when the command writes no result file.
std::string MaybeResultIdFor(const std::string& line) {
  std::string rest;
  if (ConsumePrefix(line, "NEWTAB:", &rest)) {
    // NEWTAB acks only when given a "<resultId>|<url>" form.
    size_t bar = rest.find('|');
    return bar == std::string::npos ? std::string() : rest.substr(0, bar);
  }
  if (ConsumePrefix(line, "EVAL:", &rest) ||
      ConsumePrefix(line, "LISTTABS:", &rest)) {
    return rest.substr(0, rest.find('|'));  // id is the first field.
  }
  if (ConsumePrefix(line, "WAITFOR:", &rest)) {
    // WAITFOR:<timeout>|<id>|<js> -- id is the second field.
    size_t first = rest.find('|');
    if (first == std::string::npos) {
      return std::string();
    }
    std::string after = rest.substr(first + 1);
    return after.substr(0, after.find('|'));
  }
  return std::string();
}

// Splits on the first occurrence of `sep` only -- EVAL/WAITFOR payloads are
// JavaScript and may contain '|' themselves, so a full SplitString would
// mangle them.
bool SplitOnFirst(const std::string& s,
                  char sep,
                  std::string* first,
                  std::string* rest) {
  size_t pos = s.find(sep);
  if (pos == std::string::npos) {
    return false;
  }
  *first = s.substr(0, pos);
  *rest = s.substr(pos + 1);
  return true;
}

}  // namespace

// Logs every mouse/keyboard event that reaches the target widget, whether it
// came from this watcher's injections or a real user action -- the
// "middleware" observation point discussed alongside this spike, built on
// content::RenderWidgetHost::AddInputEventObserver(). Observation-only for
// now: InputEventObserver has no way to swallow an event; a
// MouseEventCallback/KeyPressEventCallback (also public on
// content::RenderWidgetHost) would be the next step if blocking/rewriting
// real input is ever needed.
// Also a content::RenderWidgetHostObserver so it detaches safely when the
// observed widget is destroyed -- a cross-document navigation frees the old
// RenderWidgetHost, and without this the destructor would call
// RemoveInputEventObserver() on a dangling pointer (SEGV). On
// RenderWidgetHostDestroyed we null the pointer; the destructor then skips
// the removal (the widget cleaned up its own observer lists as it died).
class SendKeysWatcher::Observer
    : public content::RenderWidgetHost::InputEventObserver,
      public content::RenderWidgetHostObserver {
 public:
  explicit Observer(content::RenderWidgetHost* rwh) : rwh_(rwh) {
    rwh_->AddInputEventObserver(this);
    rwh_->AddObserver(this);
  }
  ~Observer() override {
    if (rwh_) {
      rwh_->RemoveInputEventObserver(this);
      rwh_->RemoveObserver(this);
    }
  }

  content::RenderWidgetHost* rwh() const { return rwh_; }

  // content::RenderWidgetHost::InputEventObserver:
  void OnInputEvent(const content::RenderWidgetHost& host,
                    const blink::WebInputEvent& event,
                    InputEventSource source) override {
    VLOG(2) << "sendkeys observer: event type "
           << static_cast<int>(event.GetType());
  }

  // content::RenderWidgetHostObserver:
  void RenderWidgetHostDestroyed(
      content::RenderWidgetHost* widget_host) override {
    // The widget is going away (e.g. cross-document navigation). Drop our
    // reference so ~Observer() won't touch freed memory. No need to call
    // Remove* here -- the widget tears down its own observer lists.
    rwh_ = nullptr;
  }

 private:
  raw_ptr<content::RenderWidgetHost> rwh_;
};

// NETLOG:START ... NETLOG:STOP:<path> span. Records every resource load on
// the target WebContents via the public
// content::WebContentsObserver::ResourceLoadComplete() hook -- no CDP
// Network domain needed, same "reuse the public observer Chromium already
// exposes" approach as the InputEventObserver above.
class SendKeysWatcher::NetworkLogObserver : public content::WebContentsObserver {
 public:
  explicit NetworkLogObserver(content::WebContents* contents)
      : content::WebContentsObserver(contents) {}

  void ResourceLoadComplete(
      content::RenderFrameHost* render_frame_host,
      const content::GlobalRequestID& request_id,
      const GURL& original_url,
      const blink::mojom::ResourceLoadInfo& info) override {
    base::DictValue entry;
    entry.Set("url", info.final_url.spec());
    entry.Set("original_url", original_url.spec());
    entry.Set("method", info.method);
    entry.Set("mime_type", info.mime_type);
    entry.Set("http_status_code", info.http_status_code);
    entry.Set("net_error", info.net_error);
    entries_.Append(std::move(entry));
  }

  base::ListValue TakeEntries() { return std::move(entries_); }

 private:
  base::ListValue entries_;
};

namespace {

constexpr net::NetworkTrafficAnnotationTag kAudioBridgeTrafficAnnotation =
    net::DefineNetworkTrafficAnnotation("sendkeys_audio_bridge", R"(
        semantics {
          sender: "Chromium sendkeys agent audio bridge"
          description:
            "A localhost-only (127.0.0.1) WebSocket server started on demand by "
            "the AUDIOSTART spool command. Its /mic endpoint receives raw int16 "
            "PCM audio that is fed into the fake microphone so an automated "
            "agent can speak into a page's getUserMedia() stream."
          trigger: "The AUDIOSTART:<port> spool command."
          data: "Raw int16 PCM audio samples supplied by the local agent."
          destination: LOCAL
          internal {
            contacts { email: "agent-build@example.com" }
          }
          last_reviewed: "2026-07-23"
          user_data { type: NONE }
        }
        policy {
          cookies_allowed: NO
          setting:
            "This is a build-time agent-fork feature and is not user "
            "configurable. The server binds to localhost only and is off "
            "until AUDIOSTART is issued."
          policy_exception_justification:
            "Agent build only; the socket is bound to 127.0.0.1."
        })");

}  // namespace

// Localhost-only WebSocket server that receives raw PCM on /mic and feeds it
// into the fake microphone via media::AgentAudioBridge. Constructed and used
// exclusively on the browser IO thread (owned by base::SequenceBound).
class SendKeysWatcher::AudioBridgeServer : public net::HttpServer::Delegate {
 public:
  explicit AudioBridgeServer(int port) {
    auto socket = std::make_unique<net::TCPServerSocket>(
        /*net_log=*/nullptr, net::NetLogSource());
    int rv = socket->ListenWithAddressAndPort("127.0.0.1", port, /*backlog=*/5);
    if (rv != net::OK) {
      LOG(ERROR) << "[sendkeys] AUDIOSTART: cannot listen on 127.0.0.1:" << port
                 << " (" << net::ErrorToString(rv) << ")";
      return;
    }
    server_ = std::make_unique<net::HttpServer>(std::move(socket), this);
    net::IPEndPoint local;
    if (server_->GetLocalAddress(&local) == net::OK) {
      LOG(INFO) << "[sendkeys] audio bridge listening on ws://"
                << local.ToString() << "/mic";
    }
  }

  ~AudioBridgeServer() override = default;

  AudioBridgeServer(const AudioBridgeServer&) = delete;
  AudioBridgeServer& operator=(const AudioBridgeServer&) = delete;

  // net::HttpServer::Delegate:
  void OnConnect(int /*connection_id*/) override {}
  void OnHttpRequest(int connection_id,
                     const net::HttpServerRequestInfo& /*info*/) override {
    if (server_)
      server_->Send404(connection_id, kAudioBridgeTrafficAnnotation);
  }
  void OnWebSocketRequest(int connection_id,
                          const net::HttpServerRequestInfo& info) override {
    if (server_)
      server_->AcceptWebSocket(connection_id, info,
                               kAudioBridgeTrafficAnnotation);
  }
  void OnWebSocketMessage(int /*connection_id*/, std::string data) override {
    // Payload is raw interleaved int16 mono PCM at the bridge's internal rate.
    // Copy through a typed buffer to avoid any unaligned int16 access on the
    // string storage.
    const size_t frames = data.size() / sizeof(int16_t);
    if (frames == 0)
      return;
    std::vector<int16_t> pcm(frames);
    base::as_writable_byte_span(pcm).copy_from(
        base::as_byte_span(data).first(frames * sizeof(int16_t)));
    media::AgentAudioBridge::Get().PushInterleavedInt16(
        pcm, /*channels=*/1, media::AgentAudioBridge::kInternalRate);
  }
  void OnClose(int /*connection_id*/) override {}

 private:
  std::unique_ptr<net::HttpServer> server_;
};

// Localhost-only WebSocket server that receives raw I420 frames on /cam and
// feeds them into the fake camera via media::AgentVideoBridge. Each WebSocket
// message is: int32 LE width, int32 LE height, then the tightly-packed I420
// bytes. Constructed and used exclusively on the browser IO thread.
class SendKeysWatcher::VideoBridgeServer : public net::HttpServer::Delegate {
 public:
  explicit VideoBridgeServer(int port) {
    auto socket = std::make_unique<net::TCPServerSocket>(
        /*net_log=*/nullptr, net::NetLogSource());
    int rv = socket->ListenWithAddressAndPort("127.0.0.1", port, /*backlog=*/5);
    if (rv != net::OK) {
      LOG(ERROR) << "[sendkeys] VIDEOSTART: cannot listen on 127.0.0.1:" << port
                 << " (" << net::ErrorToString(rv) << ")";
      return;
    }
    server_ = std::make_unique<net::HttpServer>(std::move(socket), this);
    net::IPEndPoint local;
    if (server_->GetLocalAddress(&local) == net::OK) {
      LOG(INFO) << "[sendkeys] video bridge listening on ws://"
                << local.ToString() << "/cam";
    }
  }

  ~VideoBridgeServer() override = default;

  VideoBridgeServer(const VideoBridgeServer&) = delete;
  VideoBridgeServer& operator=(const VideoBridgeServer&) = delete;

  // net::HttpServer::Delegate:
  void OnConnect(int /*connection_id*/) override {}
  void OnHttpRequest(int connection_id,
                     const net::HttpServerRequestInfo& /*info*/) override {
    if (server_)
      server_->Send404(connection_id, kAudioBridgeTrafficAnnotation);
  }
  void OnWebSocketRequest(int connection_id,
                          const net::HttpServerRequestInfo& info) override {
    if (server_)
      server_->AcceptWebSocket(connection_id, info,
                               kAudioBridgeTrafficAnnotation);
  }
  void OnWebSocketMessage(int /*connection_id*/, std::string data) override {
    // [int32 LE width][int32 LE height][I420 bytes]. AgentVideoBridge validates
    // the frame size against the dimensions and drops mismatches.
    base::span<const uint8_t> bytes = base::as_byte_span(data);
    if (bytes.size() < 8u)
      return;
    const uint32_t width = base::U32FromLittleEndian(bytes.subspan<0u, 4u>());
    const uint32_t height = base::U32FromLittleEndian(bytes.subspan<4u, 4u>());
    media::AgentVideoBridge::Get().PushI420(bytes.subspan(8u),
                                            static_cast<int>(width),
                                            static_cast<int>(height));
  }
  void OnClose(int /*connection_id*/) override {}

 private:
  std::unique_ptr<net::HttpServer> server_;
};

SendKeysWatcher::SendKeysWatcher() = default;

SendKeysWatcher::~SendKeysWatcher() {
  DCHECK(!thread_) << "Stop() must be called before destruction";
}

bool SendKeysWatcher::Start() {
  std::unique_ptr<base::Environment> env = base::Environment::Create();
  std::optional<std::string> dir = env->GetVar(kSpoolEnvVar);
  if (!dir || dir->empty()) {
    return false;
  }
  spool_dir_ = base::FilePath::FromASCII(*dir);
  if (!base::DirectoryExists(spool_dir_)) {
    LOG(ERROR) << "sendkeys: " << kSpoolEnvVar << "=" << *dir
              << " does not exist; not starting the watcher";
    return false;
  }

  stop_requested_.store(false);
  thread_ = std::make_unique<std::thread>(&SendKeysWatcher::WatcherThreadMain,
                                          this);
  LOG(INFO) << "sendkeys watcher started, spool dir " << *dir;
  return true;
}

void SendKeysWatcher::Stop() {
  if (!thread_) {
    return;
  }
  stop_requested_.store(true);
  thread_->join();
  thread_.reset();
  observer_.reset();
  netlog_observer_.reset();
  // Tear the media bridge servers down while the IO thread is still alive.
  audio_server_.Reset();
  media::AgentAudioBridge::Get().set_input_enabled(false);
  media::AgentAudioBridge::Get().Clear();
  video_server_.Reset();
  media::AgentVideoBridge::Get().set_input_enabled(false);
  media::AgentVideoBridge::Get().Clear();
  LOG(INFO) << "sendkeys watcher stopped";
}

void SendKeysWatcher::WatcherThreadMain() {
  while (!stop_requested_.load()) {
    if (!DrainOnce()) {
      base::PlatformThread::Sleep(kPollInterval);
    }
  }
}

bool SendKeysWatcher::DrainOnce() {
  std::vector<base::FilePath> files;
  base::FileEnumerator enumerator(spool_dir_, /*recursive=*/false,
                                  base::FileEnumerator::FILES);
  for (base::FilePath path = enumerator.Next(); !path.empty();
      path = enumerator.Next()) {
    if (path.BaseName().value().front() == '.') {
      continue;  // producer staging file, not yet published
    }
    files.push_back(path);
  }
  if (files.empty()) {
    return false;
  }
  std::sort(files.begin(), files.end());
  for (const auto& path : files) {
    ProcessFile(path);
  }
  return true;
}

void SendKeysWatcher::ProcessFile(const base::FilePath& path) {
  std::string contents;
  bool read_ok = base::ReadFileToString(path, &contents);
  // Delete before dispatch: at-most-once delivery, so a crash mid-file can't
  // replay it forever. Same tradeoff the terminal spike made.
  base::DeleteFile(path);
  if (!read_ok) {
    LOG(WARNING) << "sendkeys: failed to read " << path;
    return;
  }
  std::vector<std::string> lines = base::SplitString(
      contents, "\n", base::TRIM_WHITESPACE, base::SPLIT_WANT_NONEMPTY);
  for (auto& line : lines) {
    content::GetUIThreadTaskRunner({})->PostTask(
        FROM_HERE, base::BindOnce(&SendKeysWatcher::DispatchLineOnUIThread,
                                  weak_factory_.GetWeakPtr(), std::move(line)));
  }
}

content::WebContents* SendKeysWatcher::GetTargetWebContents() {
  BrowserWindowInterface* browser =
      GetLastActiveBrowserWindowInterfaceWithAnyProfile();
  if (!browser) {
    return nullptr;
  }
  tabs::TabInterface* tab = browser->GetActiveTabInterface();
  if (!tab) {
    return nullptr;
  }
  return tab->GetContents();
}

content::WebContents* SendKeysWatcher::ResolveTabId(const std::string& tab_id) {
  // Enumeration across all windows is authoritative: a closed tab is simply
  // absent (-> nullptr -> "unknown tabId"), so there is no registry to keep in
  // sync and no dangling pointer to guard against.
  for (BrowserWindowInterface* browser : GetAllBrowserWindowInterfaces()) {
    TabStripModel* tab_strip = browser->GetTabStripModel();
    for (int i = 0; i < tab_strip->count(); ++i) {
      content::WebContents* wc = tab_strip->GetWebContentsAt(i);
      auto* data = static_cast<AgentTabIdData*>(wc->GetUserData(kAgentTabIdKey));
      if (data && data->id == tab_id) {
        return wc;
      }
    }
  }
  return nullptr;
}

void SendKeysWatcher::InjectAudioStart(const std::string& port_str) {
  int port = 0;
  if (!base::StringToInt(port_str, &port) || port <= 0 || port > 65535) {
    LOG(WARNING) << "[sendkeys] AUDIOSTART: bad port '" << port_str << "'";
    return;
  }
  media::AgentAudioBridge::Get().Clear();
  media::AgentAudioBridge::Get().set_input_enabled(true);
  audio_server_ = base::SequenceBound<AudioBridgeServer>(
      content::GetIOThreadTaskRunner({}), port);
  LOG(INFO) << "[sendkeys] AUDIOSTART: mic bridge armed on port " << port
            << " (send int16 mono 48kHz PCM to ws://127.0.0.1:" << port
            << "/mic)";
}

void SendKeysWatcher::InjectAudioStop() {
  audio_server_.Reset();
  media::AgentAudioBridge::Get().set_input_enabled(false);
  media::AgentAudioBridge::Get().Clear();
  LOG(INFO) << "[sendkeys] AUDIOSTOP: mic bridge disarmed";
}

void SendKeysWatcher::InjectPlayWav(const std::string& path) {
  std::string wav_data;
  if (!base::ReadFileToString(base::FilePath(path), &wav_data)) {
    LOG(WARNING) << "[sendkeys] PLAYWAV: cannot read '" << path << "'";
    return;
  }
  std::unique_ptr<media::WavAudioHandler> handler =
      media::WavAudioHandler::Create(base::as_byte_span(wav_data));
  if (!handler) {
    LOG(WARNING) << "[sendkeys] PLAYWAV: not a valid WAV '" << path << "'";
    return;
  }
  const int channels = handler->GetNumChannels();
  const int rate = handler->GetSampleRate();
  const int total_frames = handler->total_frames_for_testing();
  if (channels <= 0 || rate <= 0 || total_frames <= 0) {
    LOG(WARNING) << "[sendkeys] PLAYWAV: empty/unsupported WAV '" << path << "'";
    return;
  }
  std::unique_ptr<media::AudioBus> bus =
      media::AudioBus::Create(channels, total_frames);
  size_t written = 0;
  if (!handler->CopyTo(bus.get(), &written) || written == 0) {
    LOG(WARNING) << "[sendkeys] PLAYWAV: decoded no frames from '" << path
                 << "'";
    return;
  }
  std::vector<float> mono(written);
  for (size_t i = 0; i < written; ++i) {
    float sum = 0.f;
    for (int c = 0; c < channels; ++c)
      sum += bus->channel(c)[i];
    mono[i] = sum / channels;
  }
  media::AgentAudioBridge::Get().set_input_enabled(true);
  media::AgentAudioBridge::Get().PushMonoFloat(mono, rate);
  LOG(INFO) << "[sendkeys] PLAYWAV: pushed " << written << " frames @ " << rate
            << "Hz (" << channels << "ch) from " << path;
}

void SendKeysWatcher::InjectVideoStart(const std::string& port_str) {
  int port = 0;
  if (!base::StringToInt(port_str, &port) || port <= 0 || port > 65535) {
    LOG(WARNING) << "[sendkeys] VIDEOSTART: bad port '" << port_str << "'";
    return;
  }
  media::AgentVideoBridge::Get().Clear();
  media::AgentVideoBridge::Get().set_input_enabled(true);
  video_server_ = base::SequenceBound<VideoBridgeServer>(
      content::GetIOThreadTaskRunner({}), port);
  LOG(INFO) << "[sendkeys] VIDEOSTART: camera bridge armed on port " << port
            << " (send [int32 w][int32 h][I420] frames to ws://127.0.0.1:"
            << port << "/cam)";
}

void SendKeysWatcher::InjectVideoStop() {
  video_server_.Reset();
  media::AgentVideoBridge::Get().set_input_enabled(false);
  media::AgentVideoBridge::Get().Clear();
  LOG(INFO) << "[sendkeys] VIDEOSTOP: camera bridge disarmed";
}

void SendKeysWatcher::DispatchLineOnUIThread(std::string line) {
  if (line.empty()) {
    return;
  }

  // Audio bridge commands act on the process-global mic bridge, not a tab, so
  // handle them before the active-tab lookup below (which would drop them when
  // no tab is focused).
  std::string audio_arg;
  if (ConsumePrefix(line, "AUDIOSTART:", &audio_arg)) {
    InjectAudioStart(audio_arg);
    return;
  }
  if (line == "AUDIOSTOP") {
    InjectAudioStop();
    return;
  }
  if (ConsumePrefix(line, "PLAYWAV:", &audio_arg)) {
    InjectPlayWav(audio_arg);
    return;
  }
  std::string video_arg;
  if (ConsumePrefix(line, "VIDEOSTART:", &video_arg)) {
    InjectVideoStart(video_arg);
    return;
  }
  if (line == "VIDEOSTOP") {
    InjectVideoStop();
    return;
  }

  // Optional "TAB:<tabId>|" prefix pins this line to one specific tab by its
  // stable UUID, independent of window focus or stray tabs -- the deterministic
  // targeting the last-active default cannot guarantee. A supplied-but-unknown
  // id is a hard error (the tab was closed or the fork restarted); it must NOT
  // silently fall back to last-active, or the exact about:blank race this fixes
  // would come back invisibly.
  content::WebContents* contents = nullptr;
  std::string after_tab;
  if (ConsumePrefix(line, "TAB:", &after_tab)) {
    size_t bar = after_tab.find('|');
    if (bar == std::string::npos) {
      LOG(WARNING) << "sendkeys: TAB: missing '|' in '" << line << "'";
      return;
    }
    std::string tab_id = after_tab.substr(0, bar);
    line = after_tab.substr(bar + 1);
    contents = ResolveTabId(tab_id);
    if (!contents) {
      LOG(WARNING) << "sendkeys: TAB: unknown tabId '" << tab_id
                   << "' (tab closed or fork restarted)";
      std::string result_id = MaybeResultIdFor(line);
      if (!result_id.empty()) {
        base::DictValue err;
        err.Set("ok", false);
        err.Set("error", "unknown tabId");
        WriteResultFile(result_id, std::move(err));
      }
      return;
    }
  } else {
    contents = GetTargetWebContents();
  }
  if (!contents) {
    LOG(WARNING) << "sendkeys: no active tab, dropping line: " << line;
    return;
  }
  content::RenderWidgetHost* rwh =
      contents->GetPrimaryMainFrame()->GetRenderWidgetHost();

  std::string rest;
  int x = 0;
  int y = 0;
  if (ConsumePrefix(line, "TEXT:", &rest)) {
    InjectText(rwh, rest);
  } else if (ConsumePrefix(line, "KEY:", &rest)) {
    InjectTrigger(rwh, rest);
  } else if (ConsumePrefix(line, "CLICK:", &rest) && ParseXY(rest, &x, &y)) {
    InjectClick(rwh, x, y, /*right_button=*/false);
  } else if (ConsumePrefix(line, "RIGHTCLICK:", &rest) &&
            ParseXY(rest, &x, &y)) {
    InjectClick(rwh, x, y, /*right_button=*/true);
  } else if (ConsumePrefix(line, "GOTO:", &rest)) {
    InjectGoto(contents, rest);
  } else if (ConsumePrefix(line, "SCREENSHOT:", &rest)) {
    InjectScreenshot(contents, rest);
  } else if (ConsumePrefix(line, "EVALASYNC:", &rest)) {
    InjectEvalAsync(contents->GetPrimaryMainFrame(), rest);
  } else if (ConsumePrefix(line, "EVAL:", &rest)) {
    InjectEval(contents->GetPrimaryMainFrame(), rest);
  } else if (ConsumePrefix(line, "WAITFOR:", &rest)) {
    InjectWaitFor(contents->GetPrimaryMainFrame(), rest);
  } else if (ConsumePrefix(line, "NETLOG:", &rest)) {
    std::string netlog_arg;
    if (rest == "START") {
      InjectNetLogStart(contents);
    } else if (ConsumePrefix(rest, "STOP:", &netlog_arg)) {
      InjectNetLogStop(netlog_arg);
    } else {
      LOG(WARNING) << "sendkeys: unrecognized NETLOG: line '" << line << "'";
    }
  } else if (ConsumePrefix(line, "NEWTAB:", &rest)) {
    // Tab/window commands may activate or destroy the active tab, invalidating
    // the |rwh| captured above. They don't inject input, so return before the
    // trailing observer re-attach touches a possibly-dangling |rwh|.
    InjectNewTab(rest);
    return;
  } else if (ConsumePrefix(line, "NEWWINDOW:", &rest)) {
    InjectNewWindow(rest);
    return;
  } else if (ConsumePrefix(line, "CLOSETAB:", &rest)) {
    InjectCloseTab(rest);
    return;
  } else if (ConsumePrefix(line, "SELECTTAB:", &rest)) {
    InjectSelectTab(rest);
    return;
  } else if (ConsumePrefix(line, "LISTTABS:", &rest)) {
    InjectListTabs(rest);
    return;
  } else {
    InjectText(rwh, line);
  }

  if (!observer_ || observer_->rwh() != rwh) {
    observer_ = std::make_unique<Observer>(rwh);
  }
}

void SendKeysWatcher::InjectText(content::RenderWidgetHost* rwh,
                                 const std::string& utf8) {
  if (!rwh) {
    return;
  }
  std::u16string text16 = base::UTF8ToUTF16(utf8);
  for (char16_t c : text16) {
    TypeOneCharacter(rwh, c);
  }
}

void SendKeysWatcher::InjectTrigger(content::RenderWidgetHost* rwh,
                                    const std::string& trigger) {
  if (!rwh) {
    return;
  }
  internal::ParsedTrigger parsed = internal::ParseTrigger(trigger);
  if (!parsed.ok) {
    LOG(WARNING) << "sendkeys: could not parse trigger '" << trigger << "'";
    return;
  }
  ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kRawKeyDown,
                     parsed.dom_key, parsed.dom_code, parsed.key_code,
                     parsed.modifiers);
  ForwardOneKeyEvent(rwh, blink::WebInputEvent::Type::kKeyUp, parsed.dom_key,
                     parsed.dom_code, parsed.key_code, parsed.modifiers);
}

void SendKeysWatcher::InjectClick(content::RenderWidgetHost* rwh,
                                  int x,
                                  int y,
                                  bool right_button) {
  if (!rwh) {
    return;
  }
  auto button = right_button ? blink::WebPointerProperties::Button::kRight
                             : blink::WebPointerProperties::Button::kLeft;
  rwh->ForwardMouseEvent(
      BuildMouseEvent(blink::WebInputEvent::Type::kMouseDown, button, x, y));
  rwh->ForwardMouseEvent(
      BuildMouseEvent(blink::WebInputEvent::Type::kMouseUp, button, x, y));
}

void SendKeysWatcher::InjectGoto(content::WebContents* contents,
                                 const std::string& url) {
  GURL gurl(url);
  if (!gurl.is_valid()) {
    LOG(WARNING) << "sendkeys: invalid GOTO url '" << url << "'";
    return;
  }
  content::NavigationController::LoadURLParams params(gurl);
  params.transition_type = ui::PAGE_TRANSITION_TYPED;
  contents->GetController().LoadURLWithParams(params);
}

void SendKeysWatcher::InjectNewTab(const std::string& spec) {
  // "<resultId>|<url>" requests an ack carrying the new tab's stable UUID (the
  // caller's dedicated surface for every later TAB:<id>| command). A bare
  // "<url>" (no '|') stays fire-and-forget for back-compat.
  std::string result_id;
  std::string url = spec;
  size_t bar = spec.find('|');
  if (bar != std::string::npos) {
    result_id = spec.substr(0, bar);
    url = spec.substr(bar + 1);
  }

  BrowserWindowInterface* browser =
      GetLastActiveBrowserWindowInterfaceWithAnyProfile();
  if (!browser) {
    LOG(WARNING) << "sendkeys: NEWTAB: no active browser window";
    if (!result_id.empty()) {
      base::DictValue err;
      err.Set("ok", false);
      err.Set("error", "no active browser window");
      WriteResultFile(result_id, std::move(err));
    }
    return;
  }
  GURL gurl(url.empty() ? "about:blank" : url);
  if (!gurl.is_valid()) {
    gurl = GURL("about:blank");
  }
  NavigateParams params(browser, gurl, ui::PAGE_TRANSITION_TYPED);
  params.disposition = WindowOpenDisposition::NEW_FOREGROUND_TAB;
  Navigate(&params, base::DoNothing());

  if (!result_id.empty()) {
    base::DictValue result;
    content::WebContents* wc = params.navigated_or_inserted_contents;
    if (wc) {
      result.Set("ok", true);
      result.Set("tabId", GetOrCreateTabId(wc));
    } else {
      result.Set("ok", false);
      result.Set("error", "tab not created");
    }
    WriteResultFile(result_id, std::move(result));
  }
}

void SendKeysWatcher::InjectNewWindow(const std::string& url) {
  BrowserWindowInterface* browser =
      GetLastActiveBrowserWindowInterfaceWithAnyProfile();
  if (!browser) {
    LOG(WARNING) << "sendkeys: NEWWINDOW: no active browser window";
    return;
  }
  GURL gurl(url.empty() ? "about:blank" : url);
  if (!gurl.is_valid()) {
    gurl = GURL("about:blank");
  }
  NavigateParams params(browser, gurl, ui::PAGE_TRANSITION_TYPED);
  params.disposition = WindowOpenDisposition::NEW_WINDOW;
  Navigate(&params, base::DoNothing());
}

void SendKeysWatcher::InjectCloseTab(const std::string& arg) {
  // A non-numeric arg is a stable tabId: close THAT tab in whatever window holds
  // it (the index-based path can't address a specific tab across windows).
  int probe = 0;
  if (!arg.empty() && !base::StringToInt(arg, &probe)) {
    for (BrowserWindowInterface* b : GetAllBrowserWindowInterfaces()) {
      TabStripModel* ts = b->GetTabStripModel();
      for (int i = 0; i < ts->count(); ++i) {
        auto* data = static_cast<AgentTabIdData*>(
            ts->GetWebContentsAt(i)->GetUserData(kAgentTabIdKey));
        if (data && data->id == arg) {
          ts->CloseWebContentsAt(
              i, CLOSE_USER_GESTURE | CLOSE_CREATE_HISTORICAL_TAB);
          return;
        }
      }
    }
    LOG(WARNING) << "sendkeys: CLOSETAB: unknown tabId '" << arg << "'";
    return;
  }
  BrowserWindowInterface* browser =
      GetLastActiveBrowserWindowInterfaceWithAnyProfile();
  if (!browser) {
    LOG(WARNING) << "sendkeys: CLOSETAB: no active browser window";
    return;
  }
  TabStripModel* tab_strip = browser->GetTabStripModel();
  int index = tab_strip->active_index();
  if (!arg.empty() && !base::StringToInt(arg, &index)) {
    LOG(WARNING) << "sendkeys: CLOSETAB: bad index '" << arg << "'";
    return;
  }
  if (index < 0 || index >= tab_strip->count()) {
    LOG(WARNING) << "sendkeys: CLOSETAB: index " << index << " out of range";
    return;
  }
  tab_strip->CloseWebContentsAt(
      index, CLOSE_USER_GESTURE | CLOSE_CREATE_HISTORICAL_TAB);
}

void SendKeysWatcher::InjectSelectTab(const std::string& arg) {
  // A non-numeric arg is a stable tabId: activate THAT tab in its own window.
  int probe = 0;
  if (!arg.empty() && !base::StringToInt(arg, &probe)) {
    for (BrowserWindowInterface* b : GetAllBrowserWindowInterfaces()) {
      TabStripModel* ts = b->GetTabStripModel();
      for (int i = 0; i < ts->count(); ++i) {
        auto* data = static_cast<AgentTabIdData*>(
            ts->GetWebContentsAt(i)->GetUserData(kAgentTabIdKey));
        if (data && data->id == arg) {
          ts->ActivateTabAt(i);
          return;
        }
      }
    }
    LOG(WARNING) << "sendkeys: SELECTTAB: unknown tabId '" << arg << "'";
    return;
  }
  BrowserWindowInterface* browser =
      GetLastActiveBrowserWindowInterfaceWithAnyProfile();
  if (!browser) {
    LOG(WARNING) << "sendkeys: SELECTTAB: no active browser window";
    return;
  }
  TabStripModel* tab_strip = browser->GetTabStripModel();
  int index = 0;
  if (!base::StringToInt(arg, &index)) {
    LOG(WARNING) << "sendkeys: SELECTTAB: bad index '" << arg << "'";
    return;
  }
  if (index < 0 || index >= tab_strip->count()) {
    LOG(WARNING) << "sendkeys: SELECTTAB: index " << index << " out of range";
    return;
  }
  tab_strip->ActivateTabAt(index);
}

void SendKeysWatcher::InjectListTabs(const std::string& id) {
  // Enumerate every tab across all windows and stamp each with its stable UUID
  // (minting one on first sighting), so a caller can discover the tabId to pin
  // later commands to -- the whole point of the registry.
  base::ListValue tab_list;
  int window_ordinal = 0;
  for (BrowserWindowInterface* browser : GetAllBrowserWindowInterfaces()) {
    TabStripModel* tab_strip = browser->GetTabStripModel();
    const int active = tab_strip->active_index();
    for (int i = 0; i < tab_strip->count(); ++i) {
      content::WebContents* wc = tab_strip->GetWebContentsAt(i);
      base::DictValue tab;
      tab.Set("tabId", GetOrCreateTabId(wc));
      tab.Set("window", window_ordinal);
      tab.Set("index", i);
      tab.Set("title", base::UTF16ToUTF8(wc->GetTitle()));
      tab.Set("url", wc->GetLastCommittedURL().spec());
      tab.Set("active", i == active);
      tab_list.Append(std::move(tab));
    }
    ++window_ordinal;
  }
  base::DictValue result;
  result.Set("ok", true);
  result.Set("value", std::move(tab_list));
  WriteResultFile(id, std::move(result));
}

void SendKeysWatcher::InjectScreenshot(content::WebContents* contents,
                                       const std::string& spec) {
  // Optional "<resultId>|" prefix requests an ack, so the client can verify the
  // PNG landed instead of polling a file that (on any failure) never appears --
  // the ADR 0001 screenshot bug. A bare "<path>" stays fire-and-forget.
  std::string result_id;
  std::string out_path = spec;
  size_t bar = spec.find('|');
  if (bar != std::string::npos) {
    result_id = spec.substr(0, bar);
    out_path = spec.substr(bar + 1);
  }

  // A per-session tab is usually BACKGROUNDED, and a hidden tab stops
  // compositing -- so CopyFromSurface would wait forever for a frame that never
  // arrives (the screenshot "hang"). Holding a capturer count forces the tab to
  // render offscreen (the same mechanism tab thumbnails / getDisplayMedia use).
  // Keep the handle alive until the copy completes, and poll until a surface is
  // actually available before copying.
  base::ScopedClosureRunner capture_handle = contents->IncrementCapturerCount(
      gfx::Size(), /*stay_hidden=*/false, /*stay_awake=*/true,
      /*is_activity=*/false);
  CaptureScreenshotWhenReady(
      contents->GetPrimaryMainFrame()->GetWeakDocumentPtr(),
      std::move(capture_handle), result_id, out_path,
      base::TimeTicks::Now() + base::Seconds(3));
}

void SendKeysWatcher::CaptureScreenshotWhenReady(
    content::WeakDocumentPtr doc,
    base::ScopedClosureRunner capture_handle,
    std::string id,
    std::string out_path,
    base::TimeTicks deadline) {
  content::RenderFrameHost* frame = doc.AsRenderFrameHostIfValid();
  content::WebContents* contents =
      frame ? content::WebContents::FromRenderFrameHost(frame) : nullptr;
  content::RenderWidgetHostView* view =
      contents ? contents->GetRenderWidgetHostView() : nullptr;
  if (!view) {
    if (!id.empty()) {
      base::DictValue err;
      err.Set("ok", false);
      err.Set("error", "tab gone before capture");
      WriteResultFile(id, std::move(err));
    }
    return;  // capture_handle destroyed here -> capturer count decremented
  }
  if (!view->IsSurfaceAvailableForCopy()) {
    if (base::TimeTicks::Now() >= deadline) {
      LOG(WARNING) << "sendkeys: screenshot: no surface after forcing render";
      if (!id.empty()) {
        base::DictValue err;
        err.Set("ok", false);
        err.Set("error", "no surface available for copy (timed out)");
        WriteResultFile(id, std::move(err));
      }
      return;
    }
    content::GetUIThreadTaskRunner({})->PostDelayedTask(
        FROM_HERE,
        base::BindOnce(&SendKeysWatcher::CaptureScreenshotWhenReady,
                       weak_factory_.GetWeakPtr(), std::move(doc),
                       std::move(capture_handle), std::move(id),
                       std::move(out_path), deadline),
        base::Milliseconds(50));
    return;
  }
  gfx::Size size = view->GetViewBounds().size();
  view->CopyFromSurface(
      gfx::Rect(size), size, base::TimeDelta(),
      base::BindOnce(
          [](base::WeakPtr<SendKeysWatcher> self,
             base::ScopedClosureRunner capture_handle, std::string id,
             std::string path, const content::CopyFromSurfaceResult& result) {
            // |capture_handle| stays alive until this callback returns -- the
            // tab only needs to render through the copy, not the file write.
            if (!result.has_value()) {
              LOG(WARNING) << "sendkeys: screenshot copy failed";
              if (self && !id.empty()) {
                base::DictValue err;
                err.Set("ok", false);
                err.Set("error", "surface copy failed");
                self->WriteResultFile(id, std::move(err));
              }
              return;
            }
            // PNG-encode (CPU) + write (blocking I/O) off the UI thread the
            // CopyFromSurface callback runs on, then ack back on the UI thread.
            base::ThreadPool::PostTaskAndReplyWithResult(
                FROM_HERE, {base::MayBlock()},
                base::BindOnce(
                    [](std::string path, SkBitmap bitmap) -> int {
                      std::optional<std::vector<uint8_t>> png =
                          gfx::PNGCodec::EncodeBGRASkBitmap(
                              bitmap, /*discard_transparency=*/false);
                      if (!png) {
                        LOG(WARNING) << "sendkeys: PNG encode failed";
                        return -1;
                      }
                      if (!base::WriteFile(base::FilePath::FromASCII(path),
                                           *png)) {
                        LOG(WARNING) << "sendkeys: screenshot write failed";
                        return -1;
                      }
                      return static_cast<int>(png->size());
                    },
                    path, result.value().bitmap),
                base::BindOnce(
                    [](base::WeakPtr<SendKeysWatcher> self, std::string id,
                       std::string path, int bytes) {
                      if (!self || id.empty()) {
                        return;
                      }
                      base::DictValue r;
                      if (bytes < 0) {
                        r.Set("ok", false);
                        r.Set("error", "png encode or file write failed");
                      } else {
                        r.Set("ok", true);
                        r.Set("path", path);
                        r.Set("bytes", bytes);
                      }
                      self->WriteResultFile(id, std::move(r));
                    },
                    self, id, path));
          },
          weak_factory_.GetWeakPtr(), std::move(capture_handle),
          std::move(id), std::move(out_path)));
}

void SendKeysWatcher::WriteResultFile(const std::string& id,
                                      base::DictValue result) {
  // Serialize on the current (UI) thread -- pure CPU, allowed -- but the
  // directory-create + file-write are blocking I/O, which is forbidden on the
  // UI thread (base::AssertBlockingAllowed CHECK-fails, fatal under
  // DCHECK_ALWAYS_ON). Hand the blocking part to the thread pool.
  std::optional<std::string> json = base::WriteJson(result);
  if (!json) {
    LOG(WARNING) << "sendkeys: could not serialize result for id '" << id
                 << "'";
    return;
  }
  base::FilePath out =
      spool_dir_.Append(FILE_PATH_LITERAL("results"))
          .Append(base::FilePath::FromASCII(id + ".json"));
  base::ThreadPool::PostTask(
      FROM_HERE, {base::MayBlock()},
      base::BindOnce(
          [](base::FilePath path, std::string data) {
            base::CreateDirectory(path.DirName());
            base::WriteFile(path, data);
          },
          std::move(out), std::move(*json)));
}

void SendKeysWatcher::InjectEval(content::RenderFrameHost* frame,
                                 const std::string& spec) {
  std::string id;
  std::string js;
  if (!frame || !SplitOnFirst(spec, '|', &id, &js)) {
    LOG(WARNING) << "sendkeys: malformed EVAL: line, expected id|js";
    return;
  }
  // ExecuteJavaScriptForTests() is the only public RenderFrameHost API that
  // runs unrestricted script outside chrome://devtools:// pages -- the
  // default ExecuteJavaScript() is deliberately limited to those schemes.
  // It's named/documented "ONLY FOR TESTS"; this spike uses it anyway. See
  // the spec's gotchas. Despite its name, ISOLATED_WORLD_ID_GLOBAL is value
  // 0, which content/public/common/isolated_world_ids.h documents as "the
  // main world" -- there is no separate isolated-world sandbox here, and
  // no DevTools/CDP anywhere in this call path. This runs as the page's own
  // script would: same globals, same page-injected libraries, same state.
  frame->ExecuteJavaScriptForTests(
      base::UTF8ToUTF16(js),
      base::BindOnce(
          [](base::WeakPtr<SendKeysWatcher> self, std::string id,
            base::Value value) {
            if (!self) {
              return;
            }
            base::DictValue result;
            result.Set("ok", true);
            result.Set("value", std::move(value));
            self->WriteResultFile(id, std::move(result));
          },
          weak_factory_.GetWeakPtr(), id),
      content::ISOLATED_WORLD_ID_GLOBAL);
}

void SendKeysWatcher::InjectWaitFor(content::RenderFrameHost* frame,
                                    const std::string& spec) {
  std::string timeout_str;
  std::string rest;
  std::string id;
  std::string js;
  if (!frame || !SplitOnFirst(spec, '|', &timeout_str, &rest) ||
      !SplitOnFirst(rest, '|', &id, &js)) {
    LOG(WARNING)
        << "sendkeys: malformed WAITFOR: line, expected timeout_ms|id|js";
    return;
  }
  int timeout_ms = 0;
  if (!base::StringToInt(timeout_str, &timeout_ms) || timeout_ms <= 0) {
    LOG(WARNING) << "sendkeys: bad WAITFOR timeout '" << timeout_str << "'";
    return;
  }
  PollWaitFor(frame->GetWeakDocumentPtr(), id, js,
             base::TimeTicks::Now() + base::Milliseconds(timeout_ms));
}

void SendKeysWatcher::PollWaitFor(content::WeakDocumentPtr doc,
                                  std::string id,
                                  std::string js,
                                  base::TimeTicks deadline) {
  content::RenderFrameHost* frame = doc.AsRenderFrameHostIfValid();
  if (!frame) {
    base::DictValue result;
    result.Set("ok", false);
    result.Set("error", "frame navigated away or was destroyed");
    WriteResultFile(id, std::move(result));
    return;
  }
  if (base::TimeTicks::Now() >= deadline) {
    base::DictValue result;
    result.Set("ok", false);
    result.Set("timeout", true);
    WriteResultFile(id, std::move(result));
    return;
  }
  frame->ExecuteJavaScriptForTests(
      base::UTF8ToUTF16(js),
      base::BindOnce(
          [](base::WeakPtr<SendKeysWatcher> self, content::WeakDocumentPtr doc,
            std::string id, std::string js, base::TimeTicks deadline,
            base::Value value) {
            if (!self) {
              return;
            }
            if (value.is_bool() && value.GetBool()) {
              base::DictValue result;
              result.Set("ok", true);
              self->WriteResultFile(id, std::move(result));
              return;
            }
            content::GetUIThreadTaskRunner({})->PostDelayedTask(
                FROM_HERE,
                base::BindOnce(&SendKeysWatcher::PollWaitFor, self,
                              std::move(doc), std::move(id), std::move(js),
                              deadline),
                base::Milliseconds(100));
          },
          weak_factory_.GetWeakPtr(), doc, id, js, deadline),
      content::ISOLATED_WORLD_ID_GLOBAL);  // the main world -- see InjectEval().
}

void SendKeysWatcher::InjectEvalAsync(content::RenderFrameHost* frame,
                                     const std::string& spec) {
  std::string id;
  std::string body;
  if (!frame || !SplitOnFirst(spec, '|', &id, &body)) {
    LOG(WARNING) << "sendkeys: malformed EVALASYNC: line, expected id|body";
    return;
  }
  // Opt-in CSP-safe path: a "b64:<base64>" body is decoded and its SOURCE is
  // interpolated straight into the injected main-world script below -- no
  // runtime eval()/Function(), so a page's strict script-src (LinkedIn et al.)
  // cannot block it. Unmarked bodies are left exactly as sent (the existing
  // path, incl. the client's eval(atob(...)) form), so every other caller in the
  // browser stays on the unchanged behavior. See ADR 0001, strict-CSP follow-up.
  if (body.rfind("b64:", 0) == 0) {
    std::string decoded;
    if (!base::Base64Decode(std::string_view(body).substr(4), &decoded)) {
      LOG(WARNING) << "sendkeys: EVALASYNC: bad base64 body";
      base::DictValue err;
      err.Set("ok", false);
      err.Set("error", "bad base64 body");
      WriteResultFile(id, std::move(err));
      return;
    }
    body = std::move(decoded);
  }
  // ExecuteJavaScriptForTests() cannot await a Promise returned by the script --
  // the callback would get the unresolved Promise, not its value. So run <body>
  // as an async function body (it may use await and return), stash the settled
  // {ok,value}|{ok:false,error} on a per-id global, then poll that global from
  // C++. This stash-then-poll IS the await -- lifted out of the JS client, where
  // it used to live as evalAsync's hand-rolled window-token dance.
  const std::string key = "__agent_eval_" + id;
  const std::string kick = "(function(){var k='" + key +
                           "';window[k]='__pending__';(async function(){try{"
                           "var __v=await (async function(){" +
                           body +
                           "})();window[k]={ok:true,value:__v};}catch(e){"
                           "window[k]={ok:false,error:String((e&&e.stack)||e)};"
                           "}})();})()";
  frame->ExecuteJavaScriptForTests(base::UTF8ToUTF16(kick), base::DoNothing(),
                                   content::ISOLATED_WORLD_ID_GLOBAL);
  PollEvalAsync(frame->GetWeakDocumentPtr(), id, key,
                base::TimeTicks::Now() + base::Seconds(30));
}

void SendKeysWatcher::PollEvalAsync(content::WeakDocumentPtr doc,
                                    std::string id,
                                    std::string key,
                                    base::TimeTicks deadline) {
  content::RenderFrameHost* frame = doc.AsRenderFrameHostIfValid();
  if (!frame) {
    base::DictValue result;
    result.Set("ok", false);
    result.Set("error", "frame navigated away or was destroyed");
    WriteResultFile(id, std::move(result));
    return;
  }
  if (base::TimeTicks::Now() >= deadline) {
    base::DictValue result;
    result.Set("ok", false);
    result.Set("timeout", true);
    WriteResultFile(id, std::move(result));
    frame->ExecuteJavaScriptForTests(
        base::UTF8ToUTF16("try{delete window['" + key + "']}catch(e){}"),
        base::DoNothing(), content::ISOLATED_WORLD_ID_GLOBAL);
    return;
  }
  // Returns null while pending, else the settled {ok,...} dict (and clears it).
  const std::string probe = "(function(){var k='" + key +
                            "';var v=window[k];if(v===undefined||v==='__pending"
                            "__')return null;delete window[k];return v;})()";
  frame->ExecuteJavaScriptForTests(
      base::UTF8ToUTF16(probe),
      base::BindOnce(
          [](base::WeakPtr<SendKeysWatcher> self, content::WeakDocumentPtr doc,
             std::string id, std::string key, base::TimeTicks deadline,
             base::Value value) {
            if (!self) {
              return;
            }
            if (value.is_dict()) {
              self->WriteResultFile(id, std::move(value).TakeDict());
              return;
            }
            content::GetUIThreadTaskRunner({})->PostDelayedTask(
                FROM_HERE,
                base::BindOnce(&SendKeysWatcher::PollEvalAsync, self,
                               std::move(doc), std::move(id), std::move(key),
                               deadline),
                base::Milliseconds(100));
          },
          weak_factory_.GetWeakPtr(), doc, id, key, deadline),
      content::ISOLATED_WORLD_ID_GLOBAL);  // the main world -- see InjectEval().
}

void SendKeysWatcher::InjectNetLogStart(content::WebContents* contents) {
  if (!contents) {
    return;
  }
  netlog_observer_ = std::make_unique<NetworkLogObserver>(contents);
}

void SendKeysWatcher::InjectNetLogStop(const std::string& out_path) {
  if (!netlog_observer_) {
    LOG(WARNING) << "sendkeys: NETLOG:STOP with no active NETLOG:START";
    return;
  }
  base::ListValue entries = netlog_observer_->TakeEntries();
  netlog_observer_.reset();
  std::optional<std::string> json = base::WriteJson(entries);
  if (!json) {
    LOG(WARNING) << "sendkeys: could not serialize network log";
    return;
  }
  // Blocking write off the UI thread (see WriteResultFile).
  base::ThreadPool::PostTask(
      FROM_HERE, {base::MayBlock()},
      base::BindOnce(
          [](std::string path, std::string data) {
            base::WriteFile(base::FilePath::FromASCII(path), data);
          },
          out_path, std::move(*json)));
}

}  // namespace sendkeys
