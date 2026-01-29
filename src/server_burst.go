// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && burst
// +build !js,burst

//
// server_burst.go - BurstRTC 实验用 WebRTC 服务器（工程近似版）

package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/asticode/go-astiav"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func main() {
	videoFile := flag.String("video", "", "Video file path (e.g., assets/Ultra.mp4)")
	localIP := flag.String("ip", "", "Local IP address for WebRTC (e.g., 192.168.100.1). If not specified, auto-detect")
	offerFile := flag.String("offer-file", "", "Path to file to write offer (optional, if not specified, write to stdout)")
	answerFile := flag.String("answer-file", "", "Path to file containing answer (optional, if not specified, read from stdin)")
	loop := flag.Bool("loop", false, "Loop video playback (default: false, play once)")
	sessionDir := flag.String("session-dir", "", "Session directory for this experiment (optional, used mainly by scripts)")
	safetyMargin := flag.Float64("burst-safety-margin", 0.7, "Safety margin for burst rate control (default: 0.7)")
	frameInterval := flag.Duration("burst-frame-interval", time.Second/30, "Frame interval (default: 1/30s for 30fps)")
	flag.Parse()

	if *videoFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -video parameter is required\n")
		os.Exit(1)
	}

	if *sessionDir != "" {
		if err := os.MkdirAll(*sessionDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating session directory: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stat(*videoFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: video file not found: %s\n", *videoFile)
		os.Exit(1)
	}

	absPath, err := filepath.Abs(*videoFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get absolute path: %v\n", err)
		os.Exit(1)
	}

	astiav.RegisterAllDevices()

	// WebRTC SettingEngine
	settingEngine := webrtc.SettingEngine{}
	setupWebRTCSettingEngine(&settingEngine, *localIP, 50000, 50100)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	if *localIP != "" {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (LAN mode, IP: %s, fixed port range 50000-50100)...\n", *localIP)
	} else {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (localhost mode, no STUN, fixed port range 50000-50100)...\n")
	}

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", cErr)
		}
	}()

	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	setupPeerConnectionHandlers(peerConnection, nil, func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "ICE connection established!\n")
			iceConnectedCtxCancel()
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

	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: "video/h264"}, "video", "pion",
	)
	if err != nil {
		panic(err)
	}
	if _, err = peerConnection.AddTrack(videoTrack); err != nil {
		panic(err)
	}

	opusTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1",
	)
	if err != nil {
		panic(err)
	}
	if _, err = peerConnection.AddTrack(opusTrack); err != nil {
		panic(err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for ICE gathering to complete...\n")
	<-gatherComplete
	fmt.Fprintf(os.Stderr, "ICE gathering completed\n")

	offerStr := encode(peerConnection.LocalDescription())
	if *offerFile != "" {
		if err := os.WriteFile(*offerFile, []byte(offerStr+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing offer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Offer written to file: %s (%d bytes)\n", *offerFile, len(offerStr))
	} else {
		os.Stdout.WriteString(offerStr + "\n")
		os.Stdout.Sync()
		fmt.Fprintf(os.Stderr, "Offer written to stdout (%d bytes)\n", len(offerStr))
	}

	fmt.Fprintf(os.Stderr, "Waiting for answer from client...\n")
	answer := webrtc.SessionDescription{}
	var answerStr string
	if *answerFile != "" {
		fmt.Fprintf(os.Stderr, "Reading answer from file: %s\n", *answerFile)
		answerStr = readFromFile(*answerFile)
	} else {
		answerStr = readUntilNewline()
	}
	if answerStr == "" {
		fmt.Fprintf(os.Stderr, "Error: Empty answer received\n")
		os.Exit(1)
	}
	if len(answerStr) < 100 {
		fmt.Fprintf(os.Stderr, "Error: Answer too short (%d chars), expected base64 string\n", len(answerStr))
		os.Exit(1)
	}
	decode(answerStr, &answer)
	fmt.Fprintf(os.Stderr, "Answer received, setting remote description...\n")
	if err = peerConnection.SetRemoteDescription(answer); err != nil {
		panic(fmt.Sprintf("Failed to set remote description: %v", err))
	}

	fmt.Fprintf(os.Stderr, "Waiting for ICE connection to establish...\n")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	select {
	case <-iceConnectedCtx.Done():
		fmt.Fprintf(os.Stderr, "ICE connection established, starting video streaming...\n")
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "WARNING: ICE connection timeout, starting video streaming anyway...\n")
	}

	initVideoSource(absPath)
	defer freeVideoCoding()

	// 创建 BurstRTC 控制器
	burstCtrl := NewBurstController(BurstConfig{
		FrameInterval: *frameInterval,
		SafetyMargin:  *safetyMargin,
		WindowSize:    30,
		BurstFraction: 0.3, // 默认 30% burst
	})

	// 创建 metrics CSV writer（如果 session-dir 存在）
	var metricsWriter *BurstMetricsWriter
	if *sessionDir != "" {
		csvPath := filepath.Join(*sessionDir, "burst_server_metrics.csv")
		var err error
		metricsWriter, err = NewBurstMetricsWriter(csvPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create metrics CSV writer: %v\n", err)
		} else {
			defer metricsWriter.Close()
		}
	}

	videoDone := make(chan bool, 1)
	go writeVideoToTrackBurst(videoTrack, *loop, burstCtrl, metricsWriter, videoDone)

	select {
	case <-videoDone:
		fmt.Fprintf(os.Stderr, "Video streaming completed, closing connection...\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	case <-time.After(24 * time.Hour):
		fmt.Fprintf(os.Stderr, "Timeout waiting for video completion\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	}
}

// BurstMetricsWriter 用于写入 BurstRTC server 端的 metrics CSV
type BurstMetricsWriter struct {
	mu     sync.Mutex
	writer *csv.Writer
	file   *os.File
}

// NewBurstMetricsWriter 创建一个新的 BurstRTC metrics CSV writer
func NewBurstMetricsWriter(csvPath string) (*BurstMetricsWriter, error) {
	if csvPath == "" {
		return nil, fmt.Errorf("csvPath is empty")
	}

	dir := filepath.Dir(csvPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create metrics directory: %w", err)
	}

	f, err := os.Create(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics csv: %w", err)
	}

	w := csv.NewWriter(f)

	header := []string{
		"frame_index",
		"target_bits",
		"actual_bits",
		"burst_fraction",
		"send_start_unix_ms",
		"send_end_unix_ms",
		"send_duration_ms",
		"est_capacity_bps",
		"frame_size_mean",
		"frame_size_var",
	}
	if err = w.Write(header); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to write metrics header: %w", err)
	}
	w.Flush()

	return &BurstMetricsWriter{
		writer: w,
		file:   f,
	}, nil
}

// WriteBurstMetric 写入一条 BurstRTC 帧级指标
func (m *BurstMetricsWriter) WriteBurstMetric(frameIndex, targetBits, actualBits int, burstFraction float64, sendStart, sendEnd time.Time, estCapacityBps, meanBits, varBits float64) {
	if m == nil || m.writer == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sendDuration := sendEnd.Sub(sendStart).Seconds() * 1000 // 转换为毫秒

	record := []string{
		fmt.Sprintf("%d", frameIndex),
		fmt.Sprintf("%d", targetBits),
		fmt.Sprintf("%d", actualBits),
		fmt.Sprintf("%.4f", burstFraction),
		fmt.Sprintf("%d", sendStart.UnixMilli()),
		fmt.Sprintf("%d", sendEnd.UnixMilli()),
		fmt.Sprintf("%.3f", sendDuration),
		fmt.Sprintf("%.2f", estCapacityBps),
		fmt.Sprintf("%.2f", meanBits),
		fmt.Sprintf("%.2f", varBits),
	}
	if err := m.writer.Write(record); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing BurstRTC metrics CSV: %v\n", err)
		return
	}
	m.writer.Flush()
}

// Close 关闭底层文件句柄
func (m *BurstMetricsWriter) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writer != nil {
		m.writer.Flush()
	}
	if m.file != nil {
		if err := m.file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing BurstRTC metrics CSV file: %v\n", err)
		}
	}
}

// writeVideoToTrackBurst 基于 FFmpeg 解码+编码，将 H.264 帧发送到 WebRTC video track，
// 同时为每一帧更新 BurstRTC 控制器，记录发送统计并应用 per-frame 预算控制。
func writeVideoToTrackBurst(track *webrtc.TrackLocalStaticSample, loopVideo bool, ctrl *BurstController, metricsWriter *BurstMetricsWriter, done chan<- bool) {
	frameRate := videoStream.AvgFrameRate()
	if frameRate.Num() == 0 {
		frameRate = astiav.NewRational(30, 1)
	}
	h264FrameDuration := time.Duration(float64(time.Second) * float64(frameRate.Den()) / float64(frameRate.Num()))

	ticker := time.NewTicker(h264FrameDuration)
	defer ticker.Stop()

	frameID := 0

	for range ticker.C {
		decodePacket.Unref()

		if err = inputFormatContext.ReadFrame(decodePacket); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				if loopVideo {
					if err = inputFormatContext.SeekFrame(0, 0, astiav.NewSeekFlags(astiav.SeekFlagFrame)); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to seek to beginning: %v\n", err)
						break
					}
					pts = 0
					fmt.Fprintf(os.Stderr, "Video looped, restarting from beginning...\n")
					continue
				}
				fmt.Fprintf(os.Stderr, "Video playback completed (EOF reached)\n")
				select {
				case done <- true:
				default:
				}
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading frame: %v\n", err)
			continue
		}

		if decodePacket.StreamIndex() != videoStream.Index() {
			continue
		}

		decodePacket.RescaleTs(videoStream.TimeBase(), decodeCodecContext.TimeBase())

		if err = decodeCodecContext.SendPacket(decodePacket); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending packet to decoder: %v\n", err)
			continue
		}

		for {
			if err = decodeCodecContext.ReceiveFrame(decodeFrame); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					break
				}
				fmt.Fprintf(os.Stderr, "Error receiving frame: %v\n", err)
				break
			}

			frameID++
			sendStart := time.Now()

			// 从 BurstRTC 控制器获取当前帧的预算和 burst fraction
			targetBits, burstFraction := ctrl.NextFrameBudget()

			initVideoEncoding()

			if err = softwareScaleContext.ScaleFrame(decodeFrame, scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error scaling frame: %v\n", err)
				continue
			}

			pts++
			scaledFrame.SetPts(pts)

			if err = encodeCodecContext.SendFrame(scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error sending frame to encoder: %v\n", err)
				continue
			}

			var sentBitsForFrame int

			for {
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

				data := encodePacket.Data()
				sentBitsForFrame += len(data) * 8

				// 当前版本：直接发送所有数据（后续可以按 burstFraction 实现真正的 burst/pacing 分离）
				if err = track.WriteSample(media.Sample{Data: data, Duration: h264FrameDuration}); err != nil {
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error writing sample: %v\n", err)
					continue
				}
				encodePacket.Free()
			}

			sendEnd := time.Now()

			// 更新 BurstRTC 控制器
			ctrl.UpdateStats(BurstObservation{
				FrameID:   frameID,
				SentBits:  sentBitsForFrame,
				SendStart: sendStart,
				SendEnd:   sendEnd,
			})

			// 获取统计信息用于日志和 CSV
			meanBits, varBits, availBps := ctrl.GetStats()
			fmt.Fprintf(os.Stderr, "[BurstRTC] Frame %d: sent_bits=%d, target_bits=%d, burst_frac=%.2f, mean=%.0f, var=%.0f, avail_bps=%.0f\n",
				frameID, sentBitsForFrame, targetBits, burstFraction, meanBits, varBits, availBps)

			// 写入 metrics CSV
			if metricsWriter != nil {
				metricsWriter.WriteBurstMetric(frameID, targetBits, sentBitsForFrame, burstFraction,
					sendStart, sendEnd, availBps, meanBits, varBits)
			}
		}
	}
}
