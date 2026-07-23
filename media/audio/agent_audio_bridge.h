// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local (agent build): raw-PCM microphone bridge. Not upstream behavior.
// See //CHROMIUM_SENDKEYS_SPEC.md for the design writeup and protocol.

#ifndef MEDIA_AUDIO_AGENT_AUDIO_BRIDGE_H_
#define MEDIA_AUDIO_AGENT_AUDIO_BRIDGE_H_

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

// Process-global bridge that lets the browser process feed raw PCM into the
// fake microphone that a page's getUserMedia() sees -- no BlackHole / virtual
// audio driver.
//
// Producers (browser process: the sendkeys watcher's WebSocket /mic endpoint,
// or the PLAYWAV command) call Push*; the consumer is FakeAudioInputStream's
// capture source (see ChooseSource()) which calls FillBus() on the realtime
// audio capture thread. For the single instance to be shared by both, the
// agent build forces the audio service IN-PROCESS whenever the bridge is used
// (see chrome/app/chrome_main_delegate.cc) -- otherwise FakeAudioInputStream
// runs in a separate process and can't see the browser-process producer.
//
// Audio is stored internally as mono float at kInternalRate; Push* downmix and
// resample into it, FillBus() resamples back out to the device's rate and
// replicates mono across every channel, silence-filling on underrun.
class MEDIA_EXPORT AgentAudioBridge {
 public:
  static constexpr int kInternalRate = 48000;

  static AgentAudioBridge& Get();

  AgentAudioBridge(const AgentAudioBridge&) = delete;
  AgentAudioBridge& operator=(const AgentAudioBridge&) = delete;

  // When true, FakeAudioInputStream::ChooseSource() feeds the fake mic from
  // this bridge instead of the file/beep sources. Safe from any thread.
  void set_input_enabled(bool enabled);
  bool input_enabled() const;

  // Producer side (any thread). |src_rate| is the incoming sample rate. int16
  // is interleaved (|channels| samples per frame); both variants are downmixed
  // to mono and resampled to kInternalRate before being queued.
  void PushInterleavedInt16(base::span<const int16_t> data,
                            int channels,
                            int src_rate);
  void PushMonoFloat(base::span<const float> data, int src_rate);

  // Drops any queued audio (e.g. on AUDIOSTOP).
  void Clear();

  // Consumer side (realtime audio capture thread): fill |dest| at |dest_rate|
  // from the queue, replicating the mono signal across every channel, and
  // silence-filling on underrun.
  void FillBus(AudioBus* dest, int dest_rate);

 private:
  friend class base::NoDestructor<AgentAudioBridge>;
  AgentAudioBridge();
  ~AgentAudioBridge();

  // Appends |data| (mono, |src_rate|) to ring_, resampling to kInternalRate.
  void AppendResampledMonoLocked(base::span<const float> data, int src_rate)
      EXCLUSIVE_LOCKS_REQUIRED(lock_);

  mutable base::Lock lock_;
  std::deque<float> ring_ GUARDED_BY(lock_);  // mono @ kInternalRate
  double read_pos_ GUARDED_BY(lock_) = 0.0;   // fractional read cursor
  std::atomic<bool> input_enabled_{false};
};

}  // namespace media

#endif  // MEDIA_AUDIO_AGENT_AUDIO_BRIDGE_H_
