// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local (agent build): a single-device factory that exposes one virtual
// camera backed by media::AgentVideoBridge. Selected by the fork switch
// --use-fake-video-input-only, which fakes ONLY the camera (the real mic is
// untouched). Mirror of FileVideoCaptureDeviceFactory.

#ifndef MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_FACTORY_H_
#define MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_FACTORY_H_

#include "media/capture/video/video_capture_device_factory.h"

namespace media {

class CAPTURE_EXPORT AgentVideoCaptureDeviceFactory
    : public VideoCaptureDeviceFactory {
 public:
  AgentVideoCaptureDeviceFactory() = default;
  ~AgentVideoCaptureDeviceFactory() override = default;

  VideoCaptureErrorOrDevice CreateDevice(
      const VideoCaptureDeviceDescriptor& device_descriptor) override;
  void GetDevicesInfo(GetDevicesInfoCallback callback) override;
};

}  // namespace media

#endif  // MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_FACTORY_H_
