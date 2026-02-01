// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && salsify
// +build !js,salsify
//
// server_salsify.go - Salsify 实验用 WebRTC 服务器（工程近似版）

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

	// Salsify 控制相关参数
	latencyTarget := flag.Duration("salsify-latency-target", 200*time.Millisecond, "Target end-to-end latency for Salsify controller")
	safetyMargin := flag.Float64("salsify-safety-margin", 0.7, "Safety margin for Salsify bitrate budget (0,1]")

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
	connectionClosedCtx, connectionClosedCancel := context.WithCancel(context.Background())

	setupPeerConnectionHandlers(peerConnection, nil, func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "ICE connection established!\n")
			iceConnectedCtxCancel()
		} else if connectionState == webrtc.ICEConnectionStateFailed || connectionState == webrtc.ICEConnectionStateDisconnected || connectionState == webrtc.ICEConnectionStateClosed {
			fmt.Fprintf(os.Stderr, "[Salsify] ICE connection closed/disconnected/failed, calling connectionClosedCancel()...\n")
			connectionClosedCancel()
			fmt.Fprintf(os.Stderr, "[Salsify] connectionClosedCancel() called, context should be cancelled now\n")
		}
	}, func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "Peer connection established!\n")
		} else if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			fmt.Fprintf(os.Stderr, "[Salsify] Peer connection closed/disconnected/failed, calling connectionClosedCancel()...\n")
			connectionClosedCancel()
			fmt.Fprintf(os.Stderr, "[Salsify] connectionClosedCancel() called, context should be cancelled now\n")
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

	// 创建 frame metadata writer（如果 session-dir 存在）
	var metadataWriter *FrameMetadataWriter
	if *sessionDir != "" {
		csvPath := filepath.Join(*sessionDir, "frame_metadata.csv")
		var err error
		metadataWriter, err = NewFrameMetadataWriter(csvPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create frame metadata CSV writer: %v\n", err)
		} else {
			defer metadataWriter.Close()
		}
	}

	// 创建 Salsify 控制器（目前仅基于发送侧吞吐做预算）
	ctrl := NewSalsifyController(SalsifyConfig{
		FrameInterval: time.Second / 30,
		LatencyTarget: *latencyTarget,
		SafetyMargin:  *safetyMargin,
		WindowSize:    30,
	})

	videoDone := make(chan bool, 1)
	go writeVideoToTrackSalsify(videoTrack, *loop, ctrl, videoDone, connectionClosedCtx, metadataWriter)

	select {
	case <-videoDone:
		fmt.Fprintf(os.Stderr, "Video streaming completed, closing connection...\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	case <-connectionClosedCtx.Done():
		fmt.Fprintf(os.Stderr, "[Salsify] Main: connectionClosedCtx.Done() triggered, stopping video streaming...\n")
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

// writeVideoToTrackSalsify 在现有 FFmpeg 管线基础上，增加按帧 bit 统计并喂给 SalsifyController。
// 当前版本仍然只编码单个候选，但已经按帧调用 NextFrameBudget 并打印预算，便于后续扩展为多候选选择。
func writeVideoToTrackSalsify(track *webrtc.TrackLocalStaticSample, loopVideo bool, ctrl *SalsifyController, done chan<- bool, ctx context.Context, metadataWriter *FrameMetadataWriter) {
	frameRate := videoStream.AvgFrameRate()
	if frameRate.Num() == 0 {
		frameRate = astiav.NewRational(30, 1)
	}
	h264FrameDuration := time.Duration(float64(time.Second) * float64(frameRate.Den()) / float64(frameRate.Num()))

	ticker := time.NewTicker(h264FrameDuration)
	defer ticker.Stop()

	frameID := 0

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "[Salsify] Connection closed context triggered, stopping video streaming...\n")
			select {
			case done <- true:
			default:
			}
			return
		case <-ticker.C:
			// 继续处理这一帧
		}
		
		// 检查 context 是否已取消（在 ticker 触发后再次检查）
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "[Salsify] Connection closed after ticker, stopping video streaming...\n")
			select {
			case done <- true:
			default:
			}
			return
		default:
		}
		
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
			frameSendStart := time.Now()

			// 闭环控制：获取当前帧预算
			budgetBits := ctrl.NextFrameBudget()
			fmt.Fprintf(os.Stderr, "[Salsify] Frame %d budget: %d bits\n", frameID, budgetBits)

			// 初始化缩放上下文（如果还没初始化）
			if softwareScaleContext == nil {
				initVideoEncoding()
			}

			if err = softwareScaleContext.ScaleFrame(decodeFrame, scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error scaling frame: %v\n", err)
				continue
			}

			pts++
			scaledFrame.SetPts(pts)

			// 多候选编码：生成多个不同 QP 的编码候选
			candidates, err := encodeMultipleCandidates(scaledFrame, pts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error generating encoding candidates: %v\n", err)
				continue
			}

			// 根据预算选择候选：选择不超过预算的最高质量候选
			var selectedCandidate *EncodedCandidate
			for i := range candidates {
				cand := &candidates[i]
				if cand.Bits <= budgetBits {
					// 找到不超过预算的候选，选择 QP 最低的（质量最高）
					if selectedCandidate == nil || cand.QP < selectedCandidate.QP {
						selectedCandidate = cand
					}
				}
			}

			// 如果所有候选都超预算，选择最小的一个（记录 budget violation）
			if selectedCandidate == nil {
				selectedCandidate = &candidates[len(candidates)-1] // 选择 QP 最高的（最小）
				fmt.Fprintf(os.Stderr, "[Salsify] Frame %d: All candidates exceed budget, selecting smallest (QP=%d, bits=%d)\n",
					frameID, selectedCandidate.QP, selectedCandidate.Bits)
			} else {
				fmt.Fprintf(os.Stderr, "[Salsify] Frame %d: Selected candidate QP=%d, bits=%d (budget=%d)\n",
					frameID, selectedCandidate.QP, selectedCandidate.Bits, budgetBits)
			}

			// 发送选中的候选：按 packet（NALU）边界发送
			sentBitsForFrame := selectedCandidate.Bits

			// 将候选的每个 packet（对应一个 NALU）逐个发送
			for _, pktData := range selectedCandidate.Packets {
				if err = track.WriteSample(media.Sample{Data: pktData, Duration: h264FrameDuration}); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing sample (connection may be closed): %v\n", err)
					// 如果写入失败，可能是连接已断开，退出循环
					select {
					case done <- true:
					default:
					}
					return
				}
			}

			frameSendEnd := time.Now()

			ctrl.UpdateStats(SalsifyObservation{
				FrameID:      frameID,
				SentBits:     sentBitsForFrame,
				SendStart:    frameSendStart,
				SendEnd:      frameSendEnd,
				LossDetected: false,
			})

			// 写入 frame metadata
			if metadataWriter != nil {
				metadataWriter.WriteMetadata(FrameMetadata{
					FrameID:   frameID,
					SendStart: frameSendStart,
					SendEnd:   frameSendEnd,
					FrameBits: sentBitsForFrame,
				})
			}
		}
	}
}


