// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local (agent build): raw-frame camera bridge. Not upstream behavior.
// See //CHROMIUM_SENDKEYS_SPEC.md for the design writeup and protocol.

#ifndef MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_BRIDGE_H_
#define MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_BRIDGE_H_

#include <atomic>
#include <cstdint>
#include <vector>

#include "base/containers/span.h"
#include "base/no_destructor.h"
#include "base/synchronization/lock.h"
#include "base/thread_annotations.h"
#include "media/capture/capture_export.h"

namespace media {

// Process-global bridge that lets the browser process push raw I420 frames into
// the fake camera a page's getUserMedia({video}) sees -- no OBS/virtual-cam
// driver. The counterpart of media::AgentAudioBridge for video.
//
// Producers (browser process: the sendkeys watcher's /cam WebSocket) call
// PushI420; the consumer is AgentVideoCaptureDevice's capture loop, which pulls
// the most recent frame at its frame rate via GetLatestFrame(). For the single
// instance to be shared by both, the agent build forces the video-capture
// service IN-PROCESS (chrome/app/chrome_main_delegate.cc enables
// RunVideoCaptureServiceInBrowserProcess) -- otherwise the device runs in a
// separate utility process and can't see the browser-process producer.
//
// Video is last-frame-wins (unlike audio's FIFO): the device re-emits the most
// recent frame each tick and freezes on underrun.
class CAPTURE_EXPORT AgentVideoBridge {
 public:
  struct Frame {
    std::vector<uint8_t> data;  // tightly-packed I420
    int width = 0;
    int height = 0;
  };

  static AgentVideoBridge& Get();

  AgentVideoBridge(const AgentVideoBridge&) = delete;
  AgentVideoBridge& operator=(const AgentVideoBridge&) = delete;

  // When true, the fake camera factory/device is selected and fed from here.
  void set_input_enabled(bool enabled);
  bool input_enabled() const;

  // Producer side (any thread). |data| must be a tightly-packed I420 frame of
  // exactly I420Size(width, height) bytes with even dimensions; mismatches are
  // dropped (logged). Last frame wins.
  void PushI420(base::span<const uint8_t> data, int width, int height);

  // Drops the held frame (e.g. on VIDEOSTOP).
  void Clear();

  // Consumer side (capture thread): copies the most-recent frame into |out|.
  // Returns false if no frame is held.
  bool GetLatestFrame(Frame* out);

  // Byte size of a tightly-packed I420 frame: full-res Y + two half-res planes.
  static size_t I420Size(int width, int height);

 private:
  friend class base::NoDestructor<AgentVideoBridge>;
  AgentVideoBridge();
  ~AgentVideoBridge();

  mutable base::Lock lock_;
  Frame latest_ GUARDED_BY(lock_);
  bool has_frame_ GUARDED_BY(lock_) = false;
  std::atomic<bool> input_enabled_{false};
};

}  // namespace media

#endif  // MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_BRIDGE_H_
