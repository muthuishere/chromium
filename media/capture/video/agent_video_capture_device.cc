// Copyright 2026 The Chromium Authors
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

#include "media/capture/video/agent_video_capture_device.h"

#include <utility>

#include "base/functional/bind.h"
#include "base/task/single_thread_task_runner.h"
#include "base/time/time.h"
#include "gpu/command_buffer/client/client_shared_image.h"
#include "media/base/video_frame_metadata.h"
#include "media/capture/video/agent_video_bridge.h"
#include "media/capture/video/mappable_shared_image_utils.h"
#include "third_party/libyuv/include/libyuv.h"
#include "ui/gfx/color_space.h"
#include "ui/gfx/geometry/size.h"

namespace media {

namespace {
constexpr float kFrameRate = 30.0f;
}  // namespace

AgentVideoCaptureDevice::AgentVideoCaptureDevice()
    : capture_thread_("AgentVideoCapture") {}

AgentVideoCaptureDevice::~AgentVideoCaptureDevice() {
  DCHECK(thread_checker_.CalledOnValidThread());
}

void AgentVideoCaptureDevice::AllocateAndStart(
    const VideoCaptureParams& params,
    std::unique_ptr<VideoCaptureDevice::Client> client) {
  DCHECK(thread_checker_.CalledOnValidThread());
  CHECK(!capture_thread_.IsRunning());
  capture_thread_.Start();
  capture_thread_.task_runner()->PostTask(
      FROM_HERE,
      base::BindOnce(&AgentVideoCaptureDevice::OnAllocateAndStart,
                     base::Unretained(this), params, std::move(client)));
}

void AgentVideoCaptureDevice::StopAndDeAllocate() {
  DCHECK(thread_checker_.CalledOnValidThread());
  CHECK(capture_thread_.IsRunning());
  capture_thread_.task_runner()->PostTask(
      FROM_HERE,
      base::BindOnce(&AgentVideoCaptureDevice::OnStopAndDeAllocate,
                     base::Unretained(this)));
  capture_thread_.Stop();
}

void AgentVideoCaptureDevice::OnAllocateAndStart(
    const VideoCaptureParams& params,
    std::unique_ptr<Client> client) {
  DCHECK(capture_thread_.task_runner()->BelongsToCurrentThread());
  client_ = std::move(client);
  // kGpuMemoryBuffer means the pipeline wants MappableSI/IOSurface output
  // (NV12); otherwise plain shared memory (I420 ok). Mirror FileVideoCaptureDevice.
  use_mappable_buffer_ =
      params.buffer_type == VideoCaptureBufferType::kGpuMemoryBuffer;
  // Advertised/negotiated size; the actual per-frame size rides in each
  // delivered frame, so a differently-sized pushed frame still flows.
  capture_format_.frame_size = params.requested_format.frame_size;
  capture_format_.frame_rate = kFrameRate;
  capture_format_.pixel_format = PIXEL_FORMAT_I420;
  next_frame_time_ = base::TimeTicks();
  first_ref_time_ = base::TimeTicks();
  client_->OnStarted();
  capture_thread_.task_runner()->PostTask(
      FROM_HERE, base::BindOnce(&AgentVideoCaptureDevice::OnCaptureTask,
                                base::Unretained(this)));
}

void AgentVideoCaptureDevice::OnStopAndDeAllocate() {
  DCHECK(capture_thread_.task_runner()->BelongsToCurrentThread());
  client_.reset();  // Signals OnCaptureTask to stop rescheduling.
}

void AgentVideoCaptureDevice::OnCaptureTask() {
  DCHECK(capture_thread_.task_runner()->BelongsToCurrentThread());
  if (!client_)
    return;

  const base::TimeTicks current_time = base::TimeTicks::Now();
  if (first_ref_time_.is_null())
    first_ref_time_ = current_time;

  AgentVideoBridge::Frame frame;
  if (AgentVideoBridge::Get().GetLatestFrame(&frame) && !frame.data.empty()) {
    const int w = frame.width;
    const int h = frame.height;
    const gfx::Size size(w, h);
    // Split the tightly-packed I420 frame into planes via spans (no raw pointer
    // arithmetic -- -Wunsafe-buffer-usage is -Werror here).
    const size_t y_size = static_cast<size_t>(w) * h;
    const size_t c_w = static_cast<size_t>(w) / 2;
    const size_t c_size = c_w * (static_cast<size_t>(h) / 2);
    base::span<const uint8_t> src(frame.data);
    base::span<const uint8_t> src_y = src.subspan(0u, y_size);
    base::span<const uint8_t> src_u = src.subspan(y_size, c_size);
    base::span<const uint8_t> src_v = src.subspan(y_size + c_size, c_size);

    if (use_mappable_buffer_) {
      // Convert I420 -> NV12 into a mapped shared image (IOSurface on macOS).
      scoped_refptr<gpu::ClientSharedImage> shared_image;
      VideoCaptureDevice::Client::Buffer capture_buffer;
      auto reserve_result = AllocateNV12SharedImage(client_.get(), size,
                                                    &shared_image,
                                                    &capture_buffer);
      if (reserve_result !=
          VideoCaptureDevice::Client::ReserveResult::kSucceeded) {
        client_->OnFrameDropped(
            ConvertReservationFailureToFrameDropReason(reserve_result));
      } else {
        auto scoped_mapping = shared_image->Map();
        libyuv::I420ToNV12(
            src_y.data(), w, src_u.data(), static_cast<int>(c_w), src_v.data(),
            static_cast<int>(c_w),
            scoped_mapping->GetMemoryForPlane(0).data(),
            scoped_mapping->Stride(0),
            scoped_mapping->GetMemoryForPlane(1).data(),
            scoped_mapping->Stride(1), w, h);
        VideoCaptureFormat nv12_format(size, kFrameRate, PIXEL_FORMAT_NV12);
        client_->OnIncomingCapturedBuffer(
            std::move(capture_buffer), nv12_format, current_time,
            current_time - first_ref_time_,
            /*capture_begin_timestamp=*/std::nullopt, /*metadata=*/std::nullopt);
      }
    } else {
      // Shared-memory path: hand the raw I420 to the client directly.
      VideoCaptureFormat i420_format(size, kFrameRate, PIXEL_FORMAT_I420);
      client_->OnIncomingCapturedData(
          frame.data.data(), static_cast<int>(frame.data.size()), i420_format,
          gfx::ColorSpace(), /*clockwise_rotation=*/0, /*flip_y=*/false,
          current_time, current_time - first_ref_time_,
          /*capture_begin_timestamp=*/std::nullopt, VideoFrameMetadata{});
    }
  }
  // On underrun (no frame yet) we simply skip this tick; the camera stays black
  // until the first PushI420.

  // Reschedule at a fixed frame interval, without accumulating debt when behind
  // (mirror of FileVideoCaptureDevice::OnCaptureTask).
  const base::TimeDelta frame_interval =
      base::Microseconds(1E6 / capture_format_.frame_rate);
  if (next_frame_time_.is_null()) {
    next_frame_time_ = current_time + frame_interval;
  } else {
    next_frame_time_ += frame_interval;
    if (next_frame_time_ < current_time)
      next_frame_time_ = current_time;
  }
  capture_thread_.task_runner()->PostDelayedTask(
      FROM_HERE,
      base::BindOnce(&AgentVideoCaptureDevice::OnCaptureTask,
                     base::Unretained(this)),
      next_frame_time_ - current_time);
}

}  // namespace media
