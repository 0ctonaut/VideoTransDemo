// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// client-salsify.go - Salsify 实验用 WebRTC 客户端
//
//go:build !js && salsify
// +build !js,salsify

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func main() {
	// ========== 参数解析 ==========
	outputFile := flag.String("output", "", "Output video file (H.264 Annex-B). If empty and -session-dir is set, defaults to <session-dir>/received.h264")
	localIP := flag.String("ip", "", "Local IP address (e.g., 192.168.100.2). If not specified, auto-detect")
	offerFile := flag.String("offer-file", "", "Path to file containing offer (optional, if not specified, read from stdin)")
	answerFile := flag.String("answer-file", "", "Path to file to write answer (optional, if not specified, write to stdout)")
	sessionDir := flag.String("session-dir", "", "Session directory for this experiment (optional, used mainly by scripts)")
	maxDuration := flag.Duration("max-duration", 0, "Maximum recording duration (e.g., 30s, 5m). 0 means unlimited")
	maxSize := flag.Int64("max-size", 0, "Maximum file size (MB). 0 means unlimited")
	flag.Parse()

	if *sessionDir != "" {
		if err := os.MkdirAll(*sessionDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating session directory: %v\n", err)
			os.Exit(1)
		}
	}

	// 输出文件默认：session-dir/received.h264
	if *outputFile == "" {
		if *sessionDir != "" {
			*outputFile = filepath.Join(*sessionDir, "received.h264")
		} else {
			*outputFile = "received.h264"
		}
	}

	// ========== WebRTC SettingEngine ==========
	settingEngine := webrtc.SettingEngine{}
	// Client 使用 50100-50200 端口，与 server 使用的 50000-50100 区分开
	setupWebRTCSettingEngine(&settingEngine, *localIP, 50100, 50200)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
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

	// 用于在接收协程结束时通知 main 退出
	var recvOnce sync.Once
	recvDone := make(chan struct{})

	// ========== 事件处理 ==========
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			// 定期发送 PLI，确保 server 端周期性发送关键帧
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				defer ticker.Stop()
				for range ticker.C {
					if peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
						return
					}
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if rtcpSendErr != nil {
						if strings.Contains(rtcpSendErr.Error(), "closed") {
							return
						}
						fmt.Fprintf(os.Stderr, "Error sending RTCP PLI: %v\n", rtcpSendErr)
					}
				}
			}()
		}

		codecName := strings.ToLower(strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1])
		fmt.Fprintf(os.Stderr, "Track has started, payload type %d, codec %s\n", track.PayloadType(), codecName)

		if codecName == "h264" {
			// 在单独的 goroutine 中接收并写文件，结束后通知 main
			go func() {
				writeH264ToFile(track, *outputFile, *maxDuration, *maxSize)
				recvOnce.Do(func() {
					close(recvDone)
				})
			}()
		} else {
			fmt.Fprintf(os.Stderr, "Unsupported codec: %s, only H264 is supported\n", codecName)
		}
	})

	// 设置连接状态监听，当连接断开时主动关闭 peerConnection，使 ReadRTP() 返回错误
	setupPeerConnectionHandlers(peerConnection, nil, func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed || connectionState == webrtc.ICEConnectionStateDisconnected || connectionState == webrtc.ICEConnectionStateClosed {
			fmt.Fprintf(os.Stderr, "[Salsify Client] ICE connection closed/disconnected/failed, closing peer connection...\n")
			if err := peerConnection.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing peer connection in ICE handler: %v\n", err)
			}
		}
	}, func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			fmt.Fprintf(os.Stderr, "[Salsify Client] Peer connection closed/disconnected/failed, closing peer connection...\n")
			if err := peerConnection.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing peer connection in state handler: %v\n", err)
			}
		}
	})

	// ========== 读取 Server 发送的 Offer ==========
	offer := webrtc.SessionDescription{}
	var offerStr string

	if *offerFile != "" {
		fmt.Fprintf(os.Stderr, "Reading offer from file: %s\n", *offerFile)
		offerStr = readFromFile(*offerFile)
		if offerStr == "" {
			fmt.Fprintf(os.Stderr, "Error: Empty offer read from file\n")
			os.Exit(1)
		}
	} else {
		offerStr = readUntilNewline()
	}

	decode(offerStr, &offer)

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		panic(err)
	}

	// ========== 创建 Answer ==========
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}

	<-gatherComplete

	answerStr := encode(peerConnection.LocalDescription())

	if *answerFile != "" {
		if err = os.WriteFile(*answerFile, []byte(answerStr+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing answer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Answer written to file: %s (%d bytes)\n", *answerFile, len(answerStr))
	} else {
		fmt.Println(answerStr)
	}

	// ========== 等待接收协程结束 ==========
	fmt.Fprintf(os.Stderr, "Waiting for receive loop to finish...\n")
	<-recvDone
	fmt.Fprintf(os.Stderr, "Receive loop finished, exiting client-salsify\n")
}


