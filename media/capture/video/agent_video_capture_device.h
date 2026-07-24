// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
//
// Fork-local (agent build): a VideoCaptureDevice whose frames come from the
// process-global media::AgentVideoBridge (pushed by the browser process over
// the sendkeys watcher's /cam WebSocket) instead of real hardware. Structurally
// a stripped-down FileVideoCaptureDevice (no file parser, no PTZ, no photo).

#ifndef MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_H_
#define MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_H_

#include <memory>

#include "base/threading/thread.h"
#include "base/threading/thread_checker.h"
#include "base/time/time.h"
#include "media/capture/video/video_capture_device.h"

namespace media {

class CAPTURE_EXPORT AgentVideoCaptureDevice : public VideoCaptureDevice {
 public:
  AgentVideoCaptureDevice();

  AgentVideoCaptureDevice(const AgentVideoCaptureDevice&) = delete;
  AgentVideoCaptureDevice& operator=(const AgentVideoCaptureDevice&) = delete;

  ~AgentVideoCaptureDevice() override;

  // VideoCaptureDevice:
  void AllocateAndStart(
      const VideoCaptureParams& params,
      std::unique_ptr<VideoCaptureDevice::Client> client) override;
  void StopAndDeAllocate() override;

 private:
  // All of the below run on |capture_thread_|.
  void OnAllocateAndStart(const VideoCaptureParams& params,
                          std::unique_ptr<Client> client);
  void OnStopAndDeAllocate();
  void OnCaptureTask();

  // Checks that AllocateAndStart()/StopAndDeAllocate()/dtor run on the owning
  // thread.
  base::ThreadChecker thread_checker_;

  // Active between OnAllocateAndStart() and OnStopAndDeAllocate().
  base::Thread capture_thread_;

  // The following members belong to |capture_thread_|.
  std::unique_ptr<VideoCaptureDevice::Client> client_;
  VideoCaptureFormat capture_format_;
  base::TimeTicks next_frame_time_;
  base::TimeTicks first_ref_time_;
  // True when the client asked for GpuMemoryBuffer/MappableSI output (the
  // default on macOS with the in-process capture service). Then frames are
  // converted I420 -> NV12 into a mapped shared image; otherwise the raw I420
  // is delivered as shared memory. Mirrors FileVideoCaptureDevice.
  bool use_mappable_buffer_ = false;
};

}  // namespace media

#endif  // MEDIA_CAPTURE_VIDEO_AGENT_VIDEO_CAPTURE_DEVICE_H_
