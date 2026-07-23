// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "media/audio/agent_audio_bridge.h"

#include <algorithm>
#include <vector>

#include "media/base/audio_bus.h"

namespace media {

namespace {
// Cap the queue so a stalled or absent consumer can't grow it without bound.
// ~4 seconds of mono audio at the internal rate.
constexpr size_t kMaxQueuedSamples = AgentAudioBridge::kInternalRate * 4;
}  // namespace

// static
AgentAudioBridge& AgentAudioBridge::Get() {
  static base::NoDestructor<AgentAudioBridge> instance;
  return *instance;
}

AgentAudioBridge::AgentAudioBridge() = default;
AgentAudioBridge::~AgentAudioBridge() = default;

void AgentAudioBridge::set_input_enabled(bool enabled) {
  input_enabled_.store(enabled, std::memory_order_relaxed);
}

bool AgentAudioBridge::input_enabled() const {
  return input_enabled_.load(std::memory_order_relaxed);
}

void AgentAudioBridge::PushInterleavedInt16(base::span<const int16_t> data,
                                            int channels,
                                            int src_rate) {
  if (data.empty() || channels <= 0 || src_rate <= 0)
    return;
  const size_t frames = data.size() / channels;
  if (frames == 0)
    return;
  std::vector<float> mono(frames);
  for (size_t i = 0; i < frames; ++i) {
    int sum = 0;
    for (int c = 0; c < channels; ++c)
      sum += data[i * channels + c];
    mono[i] = static_cast<float>(sum) / (channels * 32768.0f);
  }
  base::AutoLock l(lock_);
  AppendResampledMonoLocked(mono, src_rate);
}

void AgentAudioBridge::PushMonoFloat(base::span<const float> data,
                                     int src_rate) {
  if (data.empty() || src_rate <= 0)
    return;
  base::AutoLock l(lock_);
  AppendResampledMonoLocked(data, src_rate);
}

void AgentAudioBridge::Clear() {
  base::AutoLock l(lock_);
  ring_.clear();
  read_pos_ = 0.0;
}

void AgentAudioBridge::AppendResampledMonoLocked(base::span<const float> data,
                                                 int src_rate) {
  const size_t frames = data.size();
  if (frames == 0)
    return;
  if (src_rate == kInternalRate) {
    ring_.insert(ring_.end(), data.begin(), data.end());
  } else {
    // Linear resample src_rate -> kInternalRate.
    const double ratio = static_cast<double>(src_rate) / kInternalRate;
    const size_t out_frames =
        static_cast<size_t>(frames * static_cast<double>(kInternalRate) /
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
    if (read_pos_ > drop)
      read_pos_ -= drop;
    else
      read_pos_ = 0.0;
  }
}

void AgentAudioBridge::FillBus(AudioBus* dest, int dest_rate) {
  const int out_frames = dest->frames();
  const int channels = dest->channels();
  std::vector<float> mono(out_frames, 0.0f);

  {
    base::AutoLock l(lock_);
    const double step =
        static_cast<double>(kInternalRate) / static_cast<double>(dest_rate);
    double p = read_pos_;
    for (int i = 0; i < out_frames; ++i) {
      const size_t idx = static_cast<size_t>(p);
      if (idx + 1 < ring_.size()) {
        const double frac = p - idx;
        mono[i] = static_cast<float>(ring_[idx] * (1.0 - frac) +
                                     ring_[idx + 1] * frac);
        p += step;
      } else {
        // Underrun: emit silence and stop advancing so playback resumes
        // cleanly once more data arrives.
        mono[i] = 0.0f;
      }
    }
    size_t consumed = static_cast<size_t>(p);
    consumed = std::min(consumed, ring_.size());
    if (consumed > 0)
      ring_.erase(ring_.begin(), ring_.begin() + consumed);
    read_pos_ = p - consumed;
  }

  for (int c = 0; c < channels; ++c) {
    base::span<float> ch = dest->channel(c);
    for (int i = 0; i < out_frames; ++i)
      ch[i] = mono[i];
  }
}

}  // namespace media
