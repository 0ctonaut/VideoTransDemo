// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && burst
// +build !js,burst
//
// server_ffmpeg_burst.go - FFmpeg 全局状态与工具（供 BurstRTC 服务器复用）

package main

import (
	"fmt"

	"github.com/asticode/go-astiav"
)

// 与 server.go / server_ffmpeg_gcc.go / server_ffmpeg_ndtc.go 中相同的一组全局变量：
// 这些变量在整个程序运行期间都需要保持，所以定义为全局变量。
var (
	inputFormatContext   *astiav.FormatContext
	decodeCodecContext   *astiav.CodecContext
	decodePacket         *astiav.Packet
	decodeFrame          *astiav.Frame
	videoStream          *astiav.Stream
	audioStream          *astiav.Stream
	softwareScaleContext *astiav.SoftwareScaleContext
	scaledFrame          *astiav.Frame
	encodeCodecContext   *astiav.CodecContext
	encodePacket         *astiav.Packet
	pts                  int64
	err                  error
)

func initVideoSource(videoPath string) {
	if inputFormatContext = astiav.AllocFormatContext(); inputFormatContext == nil {
		panic("Failed to AllocFormatContext")
	}

	// Open input file
	if err = inputFormatContext.OpenInput(videoPath, nil, nil); err != nil {
		panic(fmt.Sprintf("Failed to open input file: %v", err))
	}

	// Find stream info
	if err = inputFormatContext.FindStreamInfo(nil); err != nil {
		panic(fmt.Sprintf("Failed to find stream info: %v", err))
	}

	// Find video stream
	for _, stream := range inputFormatContext.Streams() {
		if stream.CodecParameters().CodecType() == astiav.MediaTypeVideo {
			videoStream = stream
			break
		}
		if stream.CodecParameters().CodecType() == astiav.MediaTypeAudio {
			audioStream = stream
		}
	}

	if videoStream == nil {
		panic("No video stream found in file")
	}

	// Get decoder
	decodeCodec := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decodeCodec == nil {
		panic("FindDecoder returned nil")
	}

	if decodeCodecContext = astiav.AllocCodecContext(decodeCodec); decodeCodecContext == nil {
		panic("Failed to AllocCodecContext")
	}

	if err = videoStream.CodecParameters().ToCodecContext(decodeCodecContext); err != nil {
		panic(fmt.Sprintf("Failed to copy codec parameters: %v", err))
	}

	decodeCodecContext.SetFramerate(inputFormatContext.GuessFrameRate(videoStream, nil))

	if err = decodeCodecContext.Open(decodeCodec, nil); err != nil {
		panic(fmt.Sprintf("Failed to open decoder: %v", err))
	}

	decodePacket = astiav.AllocPacket()
	decodeFrame = astiav.AllocFrame()

	// 初始化编码器在 initVideoEncoding 中完成
}

// initVideoEncoding 与其它服务器中的实现保持一致，用于在第一次编码前初始化编码器与缩放上下文。
func initVideoEncoding() {
	if encodeCodecContext != nil {
		return
	}

	h264Encoder := astiav.FindEncoder(astiav.CodecIDH264)
	if h264Encoder == nil {
		panic("No H264 Encoder Found")
	}

	if encodeCodecContext = astiav.AllocCodecContext(h264Encoder); encodeCodecContext == nil {
		panic("Failed to AllocCodecContext Encoder")
	}

	encodeCodecContext.SetPixelFormat(astiav.PixelFormatYuv420P)
	encodeCodecContext.SetSampleAspectRatio(decodeCodecContext.SampleAspectRatio())
	encodeCodecContext.SetTimeBase(astiav.NewRational(1, 30))
	encodeCodecContext.SetWidth(decodeCodecContext.Width())
	encodeCodecContext.SetHeight(decodeCodecContext.Height())

	encodeCodecContextDictionary := astiav.NewDictionary()
	if err = encodeCodecContextDictionary.Set("preset", "ultrafast", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}
	if err = encodeCodecContextDictionary.Set("tune", "zerolatency", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}
	if err = encodeCodecContextDictionary.Set("bf", "0", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}

	if err = encodeCodecContext.Open(h264Encoder, encodeCodecContextDictionary); err != nil {
		panic(fmt.Sprintf("Failed to open encoder: %v", err))
	}

	softwareScaleContext, err = astiav.CreateSoftwareScaleContext(
		decodeCodecContext.Width(),
		decodeCodecContext.Height(),
		decodeCodecContext.PixelFormat(),
		decodeCodecContext.Width(),
		decodeCodecContext.Height(),
		astiav.PixelFormatYuv420P,
		astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create scale context: %v", err))
	}

	scaledFrame = astiav.AllocFrame()
}

// updateEncoderForBudget 根据预算 bits 动态调整编码器质量（与 NDTC 类似）
var (
	burstCurrentCRF     int = -1
	burstLastBudgetBits int = -1
)

func updateEncoderForBudgetBurst(targetBits int) error {
	const minBits = 50_000
	const maxBits = 500_000
	const minCRF = 18
	const maxCRF = 32

	var targetCRF int
	if targetBits <= minBits {
		targetCRF = maxCRF
	} else if targetBits >= maxBits {
		targetCRF = minCRF
	} else {
		ratio := float64(targetBits-minBits) / float64(maxBits-minBits)
		targetCRF = maxCRF - int(ratio*float64(maxCRF-minCRF))
	}

	// 如果 CRF 变化不大（±2），不重新配置
	if burstCurrentCRF >= 0 && absBurst(burstCurrentCRF-targetCRF) <= 2 {
		return nil
	}

	// 需要重新配置编码器
	if encodeCodecContext != nil {
		encodeCodecContext.Free()
		encodeCodecContext = nil
	}

	h264Encoder := astiav.FindEncoder(astiav.CodecIDH264)
	if h264Encoder == nil {
		return fmt.Errorf("No H264 Encoder Found")
	}

	if encodeCodecContext = astiav.AllocCodecContext(h264Encoder); encodeCodecContext == nil {
		return fmt.Errorf("Failed to AllocCodecContext Encoder")
	}

	encodeCodecContext.SetPixelFormat(astiav.PixelFormatYuv420P)
	encodeCodecContext.SetSampleAspectRatio(decodeCodecContext.SampleAspectRatio())
	encodeCodecContext.SetTimeBase(astiav.NewRational(1, 30))
	encodeCodecContext.SetWidth(decodeCodecContext.Width())
	encodeCodecContext.SetHeight(decodeCodecContext.Height())

	encodeCodecContextDictionary := astiav.NewDictionary()
	if err = encodeCodecContextDictionary.Set("preset", "ultrafast", astiav.NewDictionaryFlags()); err != nil {
		return err
	}
	if err = encodeCodecContextDictionary.Set("tune", "zerolatency", astiav.NewDictionaryFlags()); err != nil {
		return err
	}
	if err = encodeCodecContextDictionary.Set("bf", "0", astiav.NewDictionaryFlags()); err != nil {
		return err
	}
	crfStr := fmt.Sprintf("%d", targetCRF)
	if err = encodeCodecContextDictionary.Set("crf", crfStr, astiav.NewDictionaryFlags()); err != nil {
		return err
	}

	if err = encodeCodecContext.Open(h264Encoder, encodeCodecContextDictionary); err != nil {
		return fmt.Errorf("Failed to open encoder with CRF %d: %v", targetCRF, err)
	}

	burstCurrentCRF = targetCRF
	burstLastBudgetBits = targetBits
	return nil
}

func absBurst(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// freeVideoCoding 释放 FFmpeg 相关的全局状态。
func freeVideoCoding() {
	if inputFormatContext != nil {
		inputFormatContext.CloseInput()
		inputFormatContext.Free()
	}

	if decodeCodecContext != nil {
		decodeCodecContext.Free()
	}
	if decodePacket != nil {
		decodePacket.Free()
	}
	if decodeFrame != nil {
		decodeFrame.Free()
	}

	if scaledFrame != nil {
		scaledFrame.Free()
	}
	if softwareScaleContext != nil {
		softwareScaleContext.Free()
	}
	if encodeCodecContext != nil {
		encodeCodecContext.Free()
	}
	if encodePacket != nil {
		encodePacket.Free()
	}
}



