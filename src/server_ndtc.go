// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && ndtc
// +build !js,ndtc
//
// server_ndtc.go - NDTC 实验用 WebRTC 服务器（工程近似版）

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
			fmt.Fprintf(os.Stderr, "ICE connection closed/disconnected/failed, stopping video streaming...\n")
			connectionClosedCancel()
		}
	}, func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "Peer connection established!\n")
		} else if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			fmt.Fprintf(os.Stderr, "Peer connection closed/disconnected/failed, stopping video streaming...\n")
			connectionClosedCancel()
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

	// 创建 FDACE 窗口与 NDTC 控制器（当前版本仅在发送侧近似使用）
	fdaceWin := NewFdaceWindow(120)
	ndtcCtrl := NewNdtcController()

	videoDone := make(chan bool, 1)
	go writeVideoToTrackNDTC(videoTrack, *loop, fdaceWin, ndtcCtrl, videoDone, connectionClosedCtx, metadataWriter)

	select {
	case <-videoDone:
		fmt.Fprintf(os.Stderr, "Video streaming completed, closing connection...\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	case <-connectionClosedCtx.Done():
		fmt.Fprintf(os.Stderr, "Connection closed/disconnected, stopping video streaming...\n")
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

// writeVideoToTrackNDTC 基于 FFmpeg 解码+编码，将 H.264 帧发送到 WebRTC video track，
// 同时为每一帧构建 FDACE 样本并更新 NDTC 控制器。
// 当前实现只在发送侧近似使用 S≈R，因此更偏工程近似版。
func writeVideoToTrackNDTC(track *webrtc.TrackLocalStaticSample, loopVideo bool, fdaceWin *FdaceWindow, ctrl *NdtcController, done chan<- bool, ctx context.Context, metadataWriter *FrameMetadataWriter) {
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
			fmt.Fprintf(os.Stderr, "Connection closed, stopping video streaming...\n")
			select {
			case done <- true:
			default:
			}
			return
		case <-ticker.C:
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
			sendStart := time.Now()

			// 闭环控制：在编码前获取预算并调整编码器
			nextBits, pacing := ctrl.NextFrameBudget()
			
			// 初始化编码器（如果还没初始化）
			initVideoEncoding()
			
			// 根据预算调整编码器质量（闭环控制的关键步骤）
			if err = updateEncoderForBudget(nextBits); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to update encoder for budget %d: %v, using default\n", nextBits, err)
			}

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

			var sentBitsForFrame float64

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
				sentBitsForFrame += float64(len(data) * 8)

				if err = track.WriteSample(media.Sample{Data: data, Duration: h264FrameDuration}); err != nil {
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error writing sample (connection may be closed): %v\n", err)
					// 如果写入失败，可能是连接已断开，退出循环
					select {
					case done <- true:
					default:
					}
					return
				}
				encodePacket.Free()
			}

			sendEnd := time.Now()
			sendDur := sendEnd.Sub(sendStart).Seconds()

			// 使用发送持续时间近似接收持续时间，构造 FDACE 样本。
			fdaceWin.UpdateSample(FdaceSample{
				FrameID: frameID,
				S:       sendDur,
				R:       sendDur,
				L:       sentBitsForFrame,
			})

			if capBps, ok := fdaceWin.EstimateCapacity(); ok {
				ctrl.OnCapacityEstimate(capBps)
			}

			// 应用 pacing：如果 pacing 时间大于帧间隔，在帧间 sleep
			// 这样可以控制发送节奏，避免突发发送
			if pacing > h264FrameDuration {
				sleepDuration := pacing - h264FrameDuration
				if sleepDuration > 0 {
					time.Sleep(sleepDuration)
				}
			}

			fmt.Fprintf(os.Stderr, "[NDTC] Frame %d sent_bits=%.0f, target_bits=%d, pacing=%v, actual_duration=%v\n",
				frameID, sentBitsForFrame, nextBits, pacing, sendDur)

			// 写入 frame metadata
			if metadataWriter != nil {
				metadataWriter.WriteMetadata(FrameMetadata{
					FrameID:   frameID,
					SendStart: sendStart,
					SendEnd:   sendEnd,
					FrameBits: int(sentBitsForFrame),
				})
			}
		}
	}
}


