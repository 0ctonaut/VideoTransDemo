// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && salsify
// +build !js,salsify
//
// server_ffmpeg_salsify.go - FFmpeg 全局状态与工具（供 Salsify 服务器复用）

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/asticode/go-astiav"
)

// 与 server.go / server_ffmpeg_gcc.go 中相同的一组全局变量：
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

// initVideoEncoding 与 server.go / server_ffmpeg_gcc.go 中保持一致，用于在第一次编码前初始化编码器与缩放上下文。
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

// EncodedCandidate 表示一个编码候选（不同 QP 下的编码结果）
type EncodedCandidate struct {
	QP     int      // 使用的 QP 值（用于质量排序）
	Bits   int      // 编码后的比特数
	Packets [][]byte // 编码后的 H.264 packet 列表（每个 packet 对应一个 NALU）
}

// encodeFrameWithQP 使用指定的 QP 值编码一帧，返回编码后的 packet 列表和总比特数
func encodeFrameWithQP(frame *astiav.Frame, framePts int64, qp int) ([][]byte, int, error) {
	h264Encoder := astiav.FindEncoder(astiav.CodecIDH264)
	if h264Encoder == nil {
		return nil, 0, fmt.Errorf("No H264 Encoder Found")
	}

	encCtx := astiav.AllocCodecContext(h264Encoder)
	if encCtx == nil {
		return nil, 0, fmt.Errorf("Failed to AllocCodecContext Encoder")
	}
	defer encCtx.Free()

	encCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	encCtx.SetSampleAspectRatio(decodeCodecContext.SampleAspectRatio())
	encCtx.SetTimeBase(astiav.NewRational(1, 30))
	encCtx.SetWidth(decodeCodecContext.Width())
	encCtx.SetHeight(decodeCodecContext.Height())

	encDict := astiav.NewDictionary()
	if err = encDict.Set("preset", "ultrafast", astiav.NewDictionaryFlags()); err != nil {
		return nil, 0, err
	}
	if err = encDict.Set("tune", "zerolatency", astiav.NewDictionaryFlags()); err != nil {
		return nil, 0, err
	}
	if err = encDict.Set("bf", "0", astiav.NewDictionaryFlags()); err != nil {
		return nil, 0, err
	}
	// 使用固定 QP 模式
	qpStr := fmt.Sprintf("%d", qp)
	if err = encDict.Set("qp", qpStr, astiav.NewDictionaryFlags()); err != nil {
		return nil, 0, err
	}

	if err = encCtx.Open(h264Encoder, encDict); err != nil {
		return nil, 0, fmt.Errorf("Failed to open encoder with QP %d: %v", qp, err)
	}

	// 设置 PTS
	frame.SetPts(framePts)

	// 发送帧到编码器
	if err = encCtx.SendFrame(frame); err != nil {
		return nil, 0, fmt.Errorf("Error sending frame to encoder: %v", err)
	}

	// 收集所有编码后的 packet（保持 packet 边界）
	var packets [][]byte
	totalBits := 0

	for {
		pkt := astiav.AllocPacket()
		if err = encCtx.ReceivePacket(pkt); err != nil {
			if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
				pkt.Free()
				break
			}
			pkt.Free()
			return nil, 0, fmt.Errorf("Error receiving packet: %v", err)
		}

		data := pkt.Data()
		// 复制数据（因为 packet 会被释放）
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)
		packets = append(packets, dataCopy)
		totalBits += len(data) * 8
		pkt.Free()
	}

	return packets, totalBits, nil
}

// encodeMultipleCandidates 对同一帧生成多个编码候选（使用不同的 QP 值）
// 返回按 QP 排序的候选列表（QP 越低质量越高）
func encodeMultipleCandidates(frame *astiav.Frame, framePts int64) ([]EncodedCandidate, error) {
	// 定义几个 QP 档位：低 QP = 高质量，高 QP = 低质量
	qpLevels := []int{20, 25, 30, 35} // 从高质量到低质量

	var candidates []EncodedCandidate

	for _, qp := range qpLevels {
		packets, bits, err := encodeFrameWithQP(frame, framePts, qp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to encode with QP %d: %v\n", qp, err)
			continue
		}

		candidates = append(candidates, EncodedCandidate{
			QP:      qp,
			Bits:    bits,
			Packets: packets,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("Failed to generate any encoding candidates")
	}

	return candidates, nil
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


