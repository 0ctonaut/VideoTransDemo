// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// server.go - WebRTC 服务器程序
//
// 这个程序的作用：
//  1. 读取本地视频文件（支持多种格式：MP4、AVI、MKV 等）
//  2. 使用 FFmpeg 解码视频（支持 H.264、HEVC 等编码格式）
//  3. 将视频重新编码为 H.264 格式（WebRTC 标准要求）
//  4. 通过 WebRTC 发送视频流给客户端
//
// 工作流程：
//  1. 读取视频文件，使用 FFmpeg 解码
//  2. 将解码后的视频帧重新编码为 H.264
//  3. 创建 WebRTC offer（会话描述）
//  4. 等待客户端发送 answer
//  5. 建立 WebRTC 连接
//  6. 按视频帧率发送 H.264 数据包
//
// 关键概念：
//   - FFmpeg: 强大的音视频处理库，可以解码和编码各种格式
//   - 解码（Decode）: 将压缩的视频数据（如 H.264）转换为原始像素数据（YUV）
//   - 编码（Encode）: 将原始像素数据压缩为视频格式（如 H.264）
//   - 缩放（Scale）: 调整视频分辨率（如果需要）
//   - PTS（Presentation Time Stamp）: 视频帧的显示时间戳，用于控制播放速度
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asticode/go-astiav"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// ========== 全局变量：FFmpeg 相关对象 ==========
// 这些变量在整个程序运行期间都需要保持，所以定义为全局变量
var (
	inputFormatContext   *astiav.FormatContext        // 输入文件上下文：包含视频文件的所有信息（格式、流等）
	decodeCodecContext   *astiav.CodecContext         // 解码器上下文：用于解码视频
	decodePacket         *astiav.Packet               // 解码数据包：从文件读取的压缩数据
	decodeFrame          *astiav.Frame                // 解码后的帧：原始像素数据（YUV 格式）
	videoStream          *astiav.Stream               // 视频流：文件中的视频轨道
	audioStream          *astiav.Stream               // 音频流：文件中的音频轨道（当前未使用）
	softwareScaleContext *astiav.SoftwareScaleContext // 缩放上下文：用于调整视频分辨率（如果需要）
	scaledFrame          *astiav.Frame                // 缩放后的帧：调整分辨率后的像素数据
	encodeCodecContext   *astiav.CodecContext         // 编码器上下文：用于将像素数据编码为 H.264
	encodePacket         *astiav.Packet               // 编码后的数据包：H.264 压缩数据
	pts                  int64                        // 显示时间戳：用于控制视频播放速度
	err                  error                        // 错误变量：用于存储函数返回的错误
)

func main() {
	videoFile := flag.String("video", "", "Video file path (e.g., Ultra.mp4)")
	localIP := flag.String("ip", "", "Local IP address for WebRTC (e.g., 192.168.100.1). If not specified, auto-detect")
	offerFile := flag.String("offer-file", "", "Path to file to write offer (optional, if not specified, write to stdout)")
	answerFile := flag.String("answer-file", "", "Path to file containing answer (optional, if not specified, read from stdin)")
	loop := flag.Bool("loop", false, "Loop video playback (default: false, play once)")
	flag.Parse()

	if *videoFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -video parameter is required\n")
		os.Exit(1)
	}

	// Check if video file exists
	if _, err := os.Stat(*videoFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: video file not found: %s\n", *videoFile)
		os.Exit(1)
	}

	// Get absolute path for the video file
	absPath, err := filepath.Abs(*videoFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get absolute path: %v\n", err)
		os.Exit(1)
	}

	// Register all devices
	astiav.RegisterAllDevices()

	// Everything below is the Pion WebRTC API! Thanks for using it ❤️.

	// ========== 配置 WebRTC 设置引擎 ==========
	// 使用公共函数配置 SettingEngine（避免重复代码）
	// Server 使用端口范围 50000-50100
	settingEngine := webrtc.SettingEngine{}
	setupWebRTCSettingEngine(&settingEngine, *localIP, 50000, 50100)

	// Prepare the configuration
	// For localhost testing, we don't need STUN servers - host candidates are sufficient
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// Empty - rely only on host candidates for localhost communication
		},
	}

	if *localIP != "" {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (LAN mode, IP: %s, fixed port range 50000-50100)...\n", *localIP)
	} else {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (localhost mode, no STUN, fixed port range 50000-50100)...\n")
	}

	// Create API with SettingEngine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", cErr)
		}
	}()

	// Create context to wait for ICE connection
	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	// ========== 设置事件处理器 ==========
	// 使用公共函数设置默认的事件处理器
	// 但我们还需要自定义 ICE 连接状态处理器，用于通知主程序连接已建立
	setupPeerConnectionHandlers(peerConnection, nil, func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "ICE connection established!\n")
			iceConnectedCtxCancel() // 通知主程序可以开始发送视频了
		} else if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: ICE connection failed!\n")
		}
	}, func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "Peer connection established!\n")
		} else if s == webrtc.PeerConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: Peer connection failed!\n")
		}
	})

	// ========== 第九步：创建视频和音频轨道 ==========
	// Track 代表一个媒体流，可以是视频或音频
	// 我们创建 H.264 视频轨道和 Opus 音频轨道（虽然音频当前未使用）

	// 创建 H.264 视频轨道
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/h264"}, "video", "pion")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(videoTrack)
	if err != nil {
		panic(err)
	}

	// 创建 Opus 音频轨道（可选，当前未使用）
	opusTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(opusTrack)
	if err != nil {
		panic(err)
	}

	// ========== 第十步：创建 Offer（会话描述） ==========
	// Offer 包含 Server 支持的编解码器、网络地址等信息
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// ========== 第十一步：等待 ICE 候选收集完成 ==========
	// 在设置本地描述之前，先创建一个 channel 来等待 ICE 候选收集完成
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// 设置本地描述，这会启动 UDP 监听器，开始收集 ICE 候选
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	// 阻塞直到 ICE 候选收集完成
	// 这确保了 Offer 中包含所有可用的网络地址信息
	fmt.Fprintf(os.Stderr, "Waiting for ICE gathering to complete...\n")
	<-gatherComplete
	fmt.Fprintf(os.Stderr, "ICE gathering completed\n")

	// ========== 输出 Offer ==========
	// 将 Offer 编码为 base64 字符串，发送给客户端
	offerStr := encode(peerConnection.LocalDescription()) // 使用公共函数
	if *offerFile != "" {
		// 写入文件（用于自动化脚本）
		err := os.WriteFile(*offerFile, []byte(offerStr+"\n"), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing offer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Offer written to file: %s (%d bytes)\n", *offerFile, len(offerStr))
	} else {
		// 输出到 stdout（用于手动复制粘贴）
		os.Stdout.WriteString(offerStr + "\n")
		os.Stdout.Sync()
		fmt.Fprintf(os.Stderr, "Offer written to stdout (%d bytes)\n", len(offerStr))
	}

	// ========== 等待客户端的 Answer ==========
	// Answer 是客户端对 Offer 的回应，包含客户端支持的编解码器和网络地址
	fmt.Fprintf(os.Stderr, "Waiting for answer from client...\n")
	answer := webrtc.SessionDescription{}
	var answerStr string
	if *answerFile != "" {
		// 从文件读取（用于自动化脚本）
		fmt.Fprintf(os.Stderr, "Reading answer from file: %s\n", *answerFile)
		answerStr = readFromFile(*answerFile)
	} else {
		// 从 stdin 读取（用于手动复制粘贴）
		answerStr = readUntilNewline() // 使用公共函数
	}
	if answerStr == "" {
		fmt.Fprintf(os.Stderr, "Error: Empty answer received\n")
		os.Exit(1)
	}
	// 验证 Answer 格式（base64 字符串应该比较长）
	if len(answerStr) < 100 {
		fmt.Fprintf(os.Stderr, "Error: Answer too short (%d chars), expected base64 string\n", len(answerStr))
		os.Exit(1)
	}
	decode(answerStr, &answer) // 使用公共函数解码
	fmt.Fprintf(os.Stderr, "Answer received, setting remote description...\n")

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(answer)
	if err != nil {
		panic(fmt.Sprintf("Failed to set remote description: %v", err))
	}

	// ========== 第十二步：等待 ICE 连接建立 ==========
	// 在开始发送视频之前，需要先建立网络连接
	fmt.Fprintf(os.Stderr, "Waiting for ICE connection to establish...\n")
	// 添加超时，避免无限等待
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	select {
	case <-iceConnectedCtx.Done():
		// ICE 连接已建立，可以开始发送视频
		fmt.Fprintf(os.Stderr, "ICE connection established, starting video streaming...\n")
	case <-ctx.Done():
		// 超时，但继续发送视频（可能连接已经建立，只是事件未触发）
		fmt.Fprintf(os.Stderr, "WARNING: ICE connection timeout, starting video streaming anyway...\n")
	}

	// ========== 第十三步：初始化视频源 ==========
	// 打开视频文件，创建解码器
	initVideoSource(absPath)
	defer freeVideoCoding() // 程序退出时释放 FFmpeg 资源

	// ========== 第十四步：启动视频发送 ==========
	// 创建一个 channel 用于接收视频播放完成的信号
	videoDone := make(chan bool, 1)

	// 在 goroutine 中启动视频发送（不阻塞主程序）
	// writeVideoToTrack 会按视频帧率持续发送帧，直到视频播放完毕
	go writeVideoToTrack(videoTrack, *loop, videoDone)

	// ========== 第十五步：等待视频播放完成 ==========
	// 主程序在这里等待，直到视频播放完毕或超时
	select {
	case <-videoDone:
		// 视频播放完成，关闭连接
		fmt.Fprintf(os.Stderr, "Video streaming completed, closing connection...\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	case <-time.After(24 * time.Hour):
		// 安全超时（正常情况下不会触发，只是防止程序永远运行）
		fmt.Fprintf(os.Stderr, "Timeout waiting for video completion\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	}
}

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

	// Initialize encoder (will be set up after we know the frame size)
}

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

func writeVideoToTrack(track *webrtc.TrackLocalStaticSample, loopVideo bool, done chan<- bool) {
	frameRate := videoStream.AvgFrameRate()
	if frameRate.Num() == 0 {
		frameRate = astiav.NewRational(30, 1)
	}
	h264FrameDuration := time.Duration(float64(time.Second) * float64(frameRate.Den()) / float64(frameRate.Num()))

	ticker := time.NewTicker(h264FrameDuration)
	defer ticker.Stop()

	for range ticker.C {
		decodePacket.Unref()

		// Read frame from file
		if err = inputFormatContext.ReadFrame(decodePacket); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				if loopVideo {
					// Loop the video - seek to beginning
					if err = inputFormatContext.SeekFrame(0, 0, astiav.NewSeekFlags(astiav.SeekFlagFrame)); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to seek to beginning: %v\n", err)
						break
					}
					pts = 0
					fmt.Fprintf(os.Stderr, "Video looped, restarting from beginning...\n")
					continue
				} else {
					// Play once, stop when EOF
					fmt.Fprintf(os.Stderr, "Video playback completed (EOF reached)\n")
					// Send completion signal
					select {
					case done <- true:
					default:
					}
					break
				}
			}
			fmt.Fprintf(os.Stderr, "Error reading frame: %v\n", err)
			continue
		}

		// Only process video packets
		if decodePacket.StreamIndex() != videoStream.Index() {
			continue
		}

		decodePacket.RescaleTs(videoStream.TimeBase(), decodeCodecContext.TimeBase())

		// Send the packet to decoder
		if err = decodeCodecContext.SendPacket(decodePacket); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending packet to decoder: %v\n", err)
			continue
		}

		for {
			// Read Decoded Frame
			if err = decodeCodecContext.ReceiveFrame(decodeFrame); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					break
				}
				fmt.Fprintf(os.Stderr, "Error receiving frame: %v\n", err)
				break
			}

			// Init the Scaling+Encoding. Can't be started until we know info on input video
			initVideoEncoding()

			// Scale the video
			if err = softwareScaleContext.ScaleFrame(decodeFrame, scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error scaling frame: %v\n", err)
				continue
			}

			// Set PTS
			pts++
			scaledFrame.SetPts(pts)

			// Encode the frame
			if err = encodeCodecContext.SendFrame(scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error sending frame to encoder: %v\n", err)
				continue
			}

			for {
				// Read encoded packets
				encodePacket = astiav.AllocPacket()
				if err = encodeCodecContext.ReceivePacket(encodePacket); err != nil {
					if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
						encodePacket.Free()
						break
					}
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error receiving packet: %v\n", err)
					break
				}

				// Write H264 to track
				if err = track.WriteSample(media.Sample{Data: encodePacket.Data(), Duration: h264FrameDuration}); err != nil {
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error writing sample: %v\n", err)
					continue
				}

				encodePacket.Free()
			}
		}
	}
}

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

// 注意：readUntilNewline 函数已移至 common.go，避免代码重复

// readFromFile 从文件读取内容，如果文件不存在或为空，会定期检查直到超时
//
// 这个函数用于自动化脚本：server 等待 client 将 answer 写入文件
// 如果文件不存在或为空，函数会每 500ms 检查一次，最多等待 60 秒
//
// 为什么需要轮询？
//   - Client 可能比 Server 晚启动，需要等待 Client 创建文件
//   - Client 写入文件需要时间，不能立即读取
//   - 轮询可以避免 Server 一直阻塞等待
//
// 参数：
//   - filePath: 要读取的文件路径
//
// 返回：
//   - 文件内容（已去除首尾空白字符）
//   - 如果超时或文件为空，返回空字符串
//
// 使用场景：
//   - Server 使用 -answer-file 参数时，会调用这个函数等待 client 写入 answer
func readFromFile(filePath string) (in string) {
	deadline := time.Now().Add(60 * time.Second)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		// Check if file exists and has content
		data, err := os.ReadFile(filePath)
		if err == nil && len(data) > 0 {
			in = strings.TrimSpace(string(data))
			if len(in) > 0 {
				fmt.Fprintf(os.Stderr, "Answer read from file (%d bytes)\n", len(in))
				return in
			}
		}

		// Wait before next check
		time.Sleep(pollInterval)
		fmt.Fprintf(os.Stderr, "Waiting for answer file... (timeout in %v)\n", deadline.Sub(time.Now()).Round(time.Second))
	}

	fmt.Fprintf(os.Stderr, "Error: Timeout waiting for answer file: %s\n", filePath)
	return ""
}

// 注意：encode 和 decode 函数已移至 common.go，避免代码重复
