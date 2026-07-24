// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "media/capture/video/agent_video_bridge.h"

#include "base/logging.h"

namespace media {

// static
AgentVideoBridge& AgentVideoBridge::Get() {
  static base::NoDestructor<AgentVideoBridge> instance;
  return *instance;
}

AgentVideoBridge::AgentVideoBridge() = default;
AgentVideoBridge::~AgentVideoBridge() = default;

// static
size_t AgentVideoBridge::I420Size(int width, int height) {
  if (width <= 0 || height <= 0)
    return 0;
  const size_t w = static_cast<size_t>(width);
  const size_t h = static_cast<size_t>(height);
  const size_t cw = static_cast<size_t>((width + 1) / 2);
  const size_t ch = static_cast<size_t>((height + 1) / 2);
  return w * h + 2 * cw * ch;  // Y + U + V (half-res chroma)
}

void AgentVideoBridge::set_input_enabled(bool enabled) {
  input_enabled_.store(enabled, std::memory_order_relaxed);
}

bool AgentVideoBridge::input_enabled() const {
  return input_enabled_.load(std::memory_order_relaxed);
}

void AgentVideoBridge::PushI420(base::span<const uint8_t> data,
                                int width,
                                int height) {
  if (width <= 0 || height <= 0 || (width & 1) || (height & 1)) {
    LOG(WARNING) << "[agent-video] rejecting frame: bad dims " << width << "x"
                 << height << " (must be positive and even)";
    return;
  }
  const size_t expected = I420Size(width, height);
  if (data.size() != expected) {
    LOG(WARNING) << "[agent-video] rejecting frame: " << data.size()
                 << " bytes != expected I420 " << expected << " for " << width
                 << "x" << height;
    return;
  }
  base::AutoLock l(lock_);
  latest_.data.assign(data.begin(), data.end());
  latest_.width = width;
  latest_.height = height;
  has_frame_ = true;
}

void AgentVideoBridge::Clear() {
  base::AutoLock l(lock_);
  latest_.data.clear();
  latest_.width = 0;
  latest_.height = 0;
  has_frame_ = false;
}

bool AgentVideoBridge::GetLatestFrame(Frame* out) {
  base::AutoLock l(lock_);
  if (!has_frame_)
    return false;
  out->data = latest_.data;
  out->width = latest_.width;
  out->height = latest_.height;
  return true;
}

}  // namespace media
