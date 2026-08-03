// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local (agent build): raw-PCM tab-output tap. Not upstream behavior.
// The mirror image of AgentAudioBridge (which feeds the fake mic): this one
// captures a tab's RENDERED audio output so an automated agent can "hear" a
// call (e.g. a Teams meeting) without BlackHole / any virtual audio driver.
// See //CHROMIUM_SENDKEYS_SPEC.md and docs/adr/0002-teams-audio-call-agent.md.

#ifndef MEDIA_AUDIO_AGENT_AUDIO_TAP_BRIDGE_H_
#define MEDIA_AUDIO_AGENT_AUDIO_TAP_BRIDGE_H_

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <deque>

#include "base/containers/span.h"
#include "base/no_destructor.h"
#include "base/synchronization/lock.h"
#include "base/thread_annotations.h"
#include "media/base/media_export.h"

namespace media {

class AudioBus;

// Process-global bridge that captures the audio a tab renders to the speakers
// and hands it to the browser process (the sendkeys watcher's WebSocket /tap
// endpoint) as raw int16 mono PCM -- no BlackHole / virtual audio driver.
//
// Producer: services/audio's OutputController::OnMoreData pushes each rendered
// buffer here (on the realtime audio thread) when enabled(). Because the agent
// build forces the audio service IN-PROCESS (see chrome_main_delegate.cc), the
// same singleton is visible to that producer and to the browser-process WS
// consumer. COARSE by design: every OutputController's output is downmixed and
// appended into one mono ring, i.e. all tabs are mixed together -- fine for the
// single-meeting-tab use case this exists for; per-tab isolation would need the
// LoopbackCoordinator group plumbing and is deliberately out of scope for v1.
//
// Audio is stored internally as mono float at kInternalRate; PushRenderedAudio
// downmixes and resamples into it, ReadInt16Mono drains it as int16 mono.
class MEDIA_EXPORT AgentAudioTapBridge {
 public:
  static constexpr int kInternalRate = 48000;

  static AgentAudioTapBridge& Get();

  AgentAudioTapBridge(const AgentAudioTapBridge&) = delete;
  AgentAudioTapBridge& operator=(const AgentAudioTapBridge&) = delete;

  // When true, OutputController mirrors its rendered output into this bridge.
  // Safe from any thread; a relaxed atomic so the realtime path pays ~nothing
  // when the tap is off.
  void set_enabled(bool enabled);
  bool enabled() const;

  // Producer side (realtime audio thread): downmix |bus| to mono, resample from
  // |src_rate| to kInternalRate, and append to the ring.
  void PushRenderedAudio(const AudioBus& bus, int src_rate);

  // Drops any queued audio (e.g. on TAPSTART/TAPSTOP).
  void Clear();

  // Consumer side (WS/IO thread): pop up to |out.size()| frames of int16 mono
  // PCM @ kInternalRate into |out|. Returns the number of frames written (may
  // be less than requested, including 0, when the ring is short).
  size_t ReadInt16Mono(base::span<int16_t> out);

  // Frames currently available to read (mono @ kInternalRate).
  size_t AvailableFrames() const;

 private:
  friend class base::NoDestructor<AgentAudioTapBridge>;
  AgentAudioTapBridge();
  ~AgentAudioTapBridge();

  // Appends |data| (mono, |src_rate|) to ring_, resampling to kInternalRate.
  void AppendResampledMonoLocked(base::span<const float> data, int src_rate)
      EXCLUSIVE_LOCKS_REQUIRED(lock_);

  mutable base::Lock lock_;
  std::deque<float> ring_ GUARDED_BY(lock_);  // mono @ kInternalRate
  std::atomic<bool> enabled_{false};
};

}  // namespace media

#endif  // MEDIA_AUDIO_AGENT_AUDIO_TAP_BRIDGE_H_
