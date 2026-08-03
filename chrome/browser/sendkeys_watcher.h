// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local input-injection spike, not upstream Chromium behavior.
// See //CHROMIUM_SENDKEYS_SPEC.md for the design writeup and protocol.

#ifndef CHROME_BROWSER_SENDKEYS_WATCHER_H_
#define CHROME_BROWSER_SENDKEYS_WATCHER_H_

#include <atomic>
#include <memory>
#include <string>
#include <thread>

#include "base/files/file_path.h"
#include "base/functional/callback_helpers.h"
#include "base/memory/weak_ptr.h"
#include "base/threading/sequence_bound.h"
#include "base/time/time.h"
#include "base/values.h"
#include "content/public/browser/render_widget_host.h"
#include "content/public/browser/weak_document_ptr.h"

namespace content {
class RenderFrameHost;
class WebContents;
}

namespace sendkeys {

// Watches a spool directory (env var CHROMIUM_SENDKEYS_DIR) on a background
// thread for command files, and replays each line through the real browser
// input pipeline (content::RenderWidgetHost::Forward{Mouse,Keyboard}Event)
// on the UI thread -- the same entry point real hardware input reaches via
// the platform view, and the same one content::RenderWidgetHost's DevTools
// InputHandler calls into. No CDP, no remote debugging, no extra process.
//
// Also registers a content::RenderWidgetHost::InputEventObserver on the
// target widget so every event -- injected or real -- is observable from one
// place (LOG(INFO) for this spike).
class SendKeysWatcher {
 public:
  SendKeysWatcher();
  ~SendKeysWatcher();

  SendKeysWatcher(const SendKeysWatcher&) = delete;
  SendKeysWatcher& operator=(const SendKeysWatcher&) = delete;

  // Reads CHROMIUM_SENDKEYS_DIR. No-ops (returns false) if unset. Must be
  // called on the UI thread; errors (logged) if the directory doesn't exist
  // -- this never creates it.
  bool Start();

  // Stops and joins the watcher thread. Safe to call even if Start() was a
  // no-op. Must be called on the UI thread, before destruction.
  void Stop();

 private:
  // Runs on the background watcher thread until stop_requested_.
  void WatcherThreadMain();

  // One poll pass: lists the spool dir, skips dotfiles/non-regular files,
  // processes remaining names in sorted order. Returns true if any file was
  // processed (so the caller can skip the poll-interval sleep).
  bool DrainOnce();

  // Reads a file's contents, deletes it (at-most-once delivery), then posts
  // each non-empty line to the UI thread for dispatch. Runs on the
  // background thread.
  void ProcessFile(const base::FilePath& path);

  // Dispatches one line by its prefix (TEXT:/KEY:/CLICK:/RIGHTCLICK:/GOTO:/
  // SCREENSHOT:/EVAL:/WAITFOR:/NETLOG:, bare line == TEXT:). Runs on the UI
  // thread.
  void DispatchLineOnUIThread(std::string line);

  void InjectText(content::RenderWidgetHost* rwh, const std::string& utf8);
  void InjectTrigger(content::RenderWidgetHost* rwh,
                      const std::string& trigger);
  void InjectClick(content::RenderWidgetHost* rwh,
                    int x,
                    int y,
                    bool right_button);
  void InjectGoto(content::WebContents* contents, const std::string& url);

  // SCREENSHOT:[<id>|]<path> -- captures the target tab's surface to a PNG. The
  // target is usually a BACKGROUNDED per-session tab, which stops compositing,
  // so this first holds a capturer count (forcing an offscreen render) and polls
  // until a surface is available, then copies. With an <id> it acks
  // {ok,path,bytes}|{ok,error}; a bare <path> is fire-and-forget.
  void InjectScreenshot(content::WebContents* contents,
                         const std::string& spec);
  void CaptureScreenshotWhenReady(content::WeakDocumentPtr doc,
                                  base::ScopedClosureRunner capture_handle,
                                  std::string id,
                                  std::string out_path,
                                  base::TimeTicks deadline);

  // EVAL:<id>|<js> -- runs <js> in the main frame's global isolated world via
  // RenderFrameHost::ExecuteJavaScriptForTests() (the only public API that
  // can run unrestricted script outside chrome:///devtools://; see the
  // spec's gotchas for why this is labeled "ForTests" but is what this spike
  // uses anyway) and writes the JSON-serialized result to
  // <spool_dir>/results/<id>.json. This one primitive covers "get the DOM"
  // (id|document.documentElement.outerHTML), "update stuff"
  // (id|document.querySelector(...).value = ...), and "run an HTTP request
  // inside the page" (id|await fetch(url).then(r => r.text())) -- all three
  // are just JavaScript from the page's point of view.
  void InjectEval(content::RenderFrameHost* frame, const std::string& spec);

  // WAITFOR:<timeout_ms>|<id>|<js-expression> -- polls <js-expression> every
  // 100ms (via ExecuteJavaScriptForTests) until it's truthy or timeout_ms
  // elapses, then writes {"ok":true} or {"ok":false,"timeout":true} to
  // <spool_dir>/results/<id>.json. Holds a content::WeakDocumentPtr across
  // polls so a navigation away mid-wait fails the wait instead of injecting
  // into a dead frame.
  void InjectWaitFor(content::RenderFrameHost* frame, const std::string& spec);
  void PollWaitFor(content::WeakDocumentPtr doc,
                    std::string id,
                    std::string js,
                    base::TimeTicks deadline);

  // EVALASYNC:<id>|<body> -- runs <body> as an async function body (may use
  // await and return), stashes the settled result on a per-id page global, and
  // polls it from C++ (ExecuteJavaScriptForTests cannot await a returned
  // Promise). Writes {"ok":true,"value":...} or {"ok":false,"error":...} to
  // results/<id>.json. This is the native home of what the JS client used to do
  // by hand (evalAsync's window-token stash + poll).
  void InjectEvalAsync(content::RenderFrameHost* frame,
                       const std::string& spec);
  void PollEvalAsync(content::WeakDocumentPtr doc,
                     std::string id,
                     std::string key,
                     base::TimeTicks deadline);

  // NETLOG:START -- attaches a WebContentsObserver to the target WebContents
  // that records every ResourceLoadComplete (url, method, mime type, status,
  // net error) via the public content::WebContentsObserver API -- no CDP
  // Network domain needed. NETLOG:STOP:<out_path> detaches it and dumps the
  // recorded entries as a JSON array to <out_path>.
  void InjectNetLogStart(content::WebContents* contents);
  void InjectNetLogStop(const std::string& out_path);

  void WriteResultFile(const std::string& id, base::DictValue result);

  // Tab / window management on the last-active browser window. NEWTAB/NEWWINDOW
  // open |url| (blank if empty); CLOSETAB/SELECTTAB act on |index_str| (the
  // active tab if empty/CLOSETAB); LISTTABS writes a results/<id>.json array of
  // {index,title,url,active}.
  void InjectNewTab(const std::string& url);
  void InjectNewWindow(const std::string& url);
  void InjectCloseTab(const std::string& index_str);
  void InjectSelectTab(const std::string& index_str);
  void InjectListTabs(const std::string& id);

  // Raw-PCM microphone bridge (agent build). AUDIOSTART:<port> boots a
  // localhost-only WebSocket server (net::HttpServer) whose /mic endpoint
  // receives raw interleaved int16 mono 48kHz PCM and feeds it into the fake
  // microphone that getUserMedia() sees, via media::AgentAudioBridge.
  // AUDIOSTOP tears it down. PLAYWAV:<path> pushes a 16-bit PCM WAV file into
  // the same bridge one-shot (no socket needed). All require launching with
  // --use-fake-device-for-media-stream so the page's mic is the fake device
  // this bridge backs. See //CHROMIUM_SENDKEYS_SPEC.md.
  void InjectAudioStart(const std::string& port_str);
  void InjectAudioStop();
  void InjectPlayWav(const std::string& path);

  // Raw-frame camera bridge (agent build). VIDEOSTART:<port> boots a
  // localhost-only WebSocket server whose /cam endpoint receives raw I420
  // frames (each message = int32 LE width, int32 LE height, then the I420
  // bytes) and feeds them to the fake camera getUserMedia({video}) sees, via
  // media::AgentVideoBridge. VIDEOSTOP tears it down. Requires the fork's
  // default --use-fake-video-input-only (real mic untouched).
  void InjectVideoStart(const std::string& port_str);
  void InjectVideoStop();

  // Raw-PCM tab-output tap (agent build). TAPSTART:<port> boots a localhost-only
  // WebSocket server whose /tap endpoint STREAMS the tab's rendered audio output
  // to the client as raw int16 mono 48kHz PCM (via media::AgentAudioTapBridge),
  // so an agent can "hear" a call. TAPSTOP tears it down. No launch flag needed;
  // the tap reads the real rendered output. See //CHROMIUM_SENDKEYS_SPEC.md.
  void InjectTapStart(const std::string& port_str);
  void InjectTapStop();

  // Resolves the active tab's WebContents via
  // GetLastActiveBrowserWindowInterfaceWithAnyProfile(). May return nullptr.
  content::WebContents* GetTargetWebContents();

  // Resolves a tab by its stable UUID (minted by GetOrCreateTabId, surfaced via
  // NEWTAB/LISTTABS) by enumerating all windows. Returns nullptr when no live
  // tab carries that id (closed, or the fork restarted) -- callers must treat
  // that as an error, never as "use the active tab".
  content::WebContents* ResolveTabId(const std::string& tab_id);

  base::FilePath spool_dir_;
  std::unique_ptr<std::thread> thread_;
  std::atomic<bool> stop_requested_{false};

  // Attached to whatever widget most recently received an injected event, so
  // OnInputEvent logs show both injected and real activity on it. Torn down
  // and reattached per-target rather than tracked across navigations -- see
  // the spec's "known gotchas" section.
  class Observer;
  std::unique_ptr<Observer> observer_;

  // Attached only while a NETLOG:START...NETLOG:STOP span is active.
  class NetworkLogObserver;
  std::unique_ptr<NetworkLogObserver> netlog_observer_;

  // Owns the localhost WebSocket audio server (net::HttpServer) while an
  // AUDIOSTART..AUDIOSTOP span is active. Lives on the browser IO thread --
  // constructed and destroyed there via base::SequenceBound.
  class AudioBridgeServer;
  base::SequenceBound<AudioBridgeServer> audio_server_;

  // Owns the localhost WebSocket camera server while a VIDEOSTART..VIDEOSTOP
  // span is active. Lives on the browser IO thread via base::SequenceBound.
  class VideoBridgeServer;
  base::SequenceBound<VideoBridgeServer> video_server_;

  // Owns the localhost WebSocket tap server (net::HttpServer) while a
  // TAPSTART..TAPSTOP span is active. It drains media::AgentAudioTapBridge on a
  // timer and pushes int16 PCM to connected clients. Lives on the browser IO
  // thread via base::SequenceBound.
  class AudioTapServer;
  base::SequenceBound<AudioTapServer> tap_server_;

  base::WeakPtrFactory<SendKeysWatcher> weak_factory_{this};
};

}  // namespace sendkeys

#endif  // CHROME_BROWSER_SENDKEYS_WATCHER_H_
