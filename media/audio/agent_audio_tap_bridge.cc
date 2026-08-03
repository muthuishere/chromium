// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "media/audio/agent_audio_tap_bridge.h"

#include <algorithm>
#include <cmath>
#include <vector>

#include "media/base/audio_bus.h"

namespace media {

namespace {
// Cap the queue so an absent/slow consumer can't grow it without bound.
// ~4 seconds of mono audio at the internal rate.
constexpr size_t kMaxQueuedSamples = AgentAudioTapBridge::kInternalRate * 4;

int16_t FloatToInt16(float v) {
  const float clamped = std::clamp(v, -1.0f, 1.0f);
  // 32767 (not 32768) keeps +1.0f from wrapping to -32768.
  return static_cast<int16_t>(std::lround(clamped * 32767.0f));
}
}  // namespace

// static
AgentAudioTapBridge& AgentAudioTapBridge::Get() {
  static base::NoDestructor<AgentAudioTapBridge> instance;
  return *instance;
}

AgentAudioTapBridge::AgentAudioTapBridge() = default;
AgentAudioTapBridge::~AgentAudioTapBridge() = default;

void AgentAudioTapBridge::set_enabled(bool enabled) {
  enabled_.store(enabled, std::memory_order_relaxed);
}

bool AgentAudioTapBridge::enabled() const {
  return enabled_.load(std::memory_order_relaxed);
}

void AgentAudioTapBridge::PushRenderedAudio(const AudioBus& bus, int src_rate) {
  const int frames = bus.frames();
  const int channels = bus.channels();
  if (frames <= 0 || channels <= 0 || src_rate <= 0)
    return;

  std::vector<float> mono(static_cast<size_t>(frames), 0.0f);
  for (int c = 0; c < channels; ++c) {
    base::span<const float> ch = bus.channel(c);
    for (int i = 0; i < frames; ++i)
      mono[static_cast<size_t>(i)] += ch[static_cast<size_t>(i)];
  }
  const float inv = 1.0f / static_cast<float>(channels);
  for (float& s : mono)
    s *= inv;

  base::AutoLock l(lock_);
  AppendResampledMonoLocked(mono, src_rate);
}

void AgentAudioTapBridge::Clear() {
  base::AutoLock l(lock_);
  ring_.clear();
}

void AgentAudioTapBridge::AppendResampledMonoLocked(base::span<const float> data,
                                                    int src_rate) {
  const size_t frames = data.size();
  if (frames == 0)
    return;
  if (src_rate == kInternalRate) {
    ring_.insert(ring_.end(), data.begin(), data.end());
  } else {
    // Linear resample src_rate -> kInternalRate.
    const double ratio = static_cast<double>(src_rate) / kInternalRate;
    const size_t out_frames = static_cast<size_t>(
        frames * static_cast<double>(kInternalRate) /
        static_cast<double>(src_rate));
    for (size_t i = 0; i < out_frames; ++i) {
      const double src = i * ratio;
      size_t idx = static_cast<size_t>(src);
      if (idx >= frames)
        idx = frames - 1;
      const double frac = src - idx;
      const float s0 = data[idx];
      const float s1 = (idx + 1 < frames) ? data[idx + 1] : s0;
      ring_.push_back(static_cast<float>(s0 * (1.0 - frac) + s1 * frac));
    }
  }

  if (ring_.size() > kMaxQueuedSamples) {
    const size_t drop = ring_.size() - kMaxQueuedSamples;
    ring_.erase(ring_.begin(), ring_.begin() + drop);
  }
}

size_t AgentAudioTapBridge::ReadInt16Mono(base::span<int16_t> out) {
  if (out.empty())
    return 0;
  base::AutoLock l(lock_);
  const size_t n = std::min(out.size(), ring_.size());
  for (size_t i = 0; i < n; ++i)
    out[i] = FloatToInt16(ring_[i]);
  if (n > 0)
    ring_.erase(ring_.begin(), ring_.begin() + n);
  return n;
}

size_t AgentAudioTapBridge::AvailableFrames() const {
  base::AutoLock l(lock_);
  return ring_.size();
}

}  // namespace media
