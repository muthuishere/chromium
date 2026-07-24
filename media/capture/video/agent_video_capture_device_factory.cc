// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "media/capture/video/agent_video_capture_device_factory.h"

#include <utility>

#include "build/build_config.h"
#include "media/capture/video/agent_video_capture_device.h"
#include "ui/gfx/geometry/size.h"

namespace media {

namespace {
constexpr char kAgentVideoDeviceId[] = "agent-virtual-camera";
constexpr char kAgentVideoDisplayName[] = "Agent Virtual Camera";
constexpr float kFrameRate = 30.0f;
}  // namespace

VideoCaptureErrorOrDevice AgentVideoCaptureDeviceFactory::CreateDevice(
    const VideoCaptureDeviceDescriptor& device_descriptor) {
  DCHECK(thread_checker_.CalledOnValidThread());
  return VideoCaptureErrorOrDevice(
      std::make_unique<AgentVideoCaptureDevice>());
}

void AgentVideoCaptureDeviceFactory::GetDevicesInfo(
    GetDevicesInfoCallback callback) {
  DCHECK(thread_checker_.CalledOnValidThread());

  auto api =
#if BUILDFLAG(IS_WIN)
      VideoCaptureApi::WIN_DIRECT_SHOW;
#elif BUILDFLAG(IS_MAC)
      VideoCaptureApi::MACOSX_AVFOUNDATION;
#elif BUILDFLAG(IS_LINUX) || BUILDFLAG(IS_CHROMEOS)
      VideoCaptureApi::LINUX_V4L2_SINGLE_PLANE;
#else
      VideoCaptureApi::UNKNOWN;
#endif

  // No pan/tilt/zoom on the virtual camera.
  VideoCaptureControlSupport control_support;

  std::vector<VideoCaptureDeviceInfo> devices_info;
  devices_info.emplace_back(VideoCaptureDeviceDescriptor(
      kAgentVideoDisplayName, kAgentVideoDeviceId, api, control_support));

  // Advertise a fixed format set (the bridge has no file to learn one from);
  // the actual per-frame size still rides in each delivered frame.
  auto& formats = devices_info.back().supported_formats;
  formats.emplace_back(gfx::Size(640, 480), kFrameRate, PIXEL_FORMAT_I420);
  formats.emplace_back(gfx::Size(1280, 720), kFrameRate, PIXEL_FORMAT_I420);

  std::move(callback).Run(std::move(devices_info));
}

}  // namespace media
