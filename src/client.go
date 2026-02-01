// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js && !gcc
// +build !js,!gcc

// client.go - WebRTC 客户端程序
//
// 这个程序的作用：
//  1. 连接到 WebRTC 服务器
//  2. 接收服务器发送的视频流（H.264 格式）
//  3. 将接收到的视频数据保存为 .h264 文件
//
// 工作流程：
//  1. 从 stdin 或文件读取 server 发送的 offer（会话描述）
//  2. 创建 answer（应答）并发送回 server
//  3. 建立 WebRTC 连接
//  4. 接收视频数据包（RTP 格式）
//  5. 解析 RTP 数据包，提取 H.264 视频数据
//  6. 将 H.264 数据写入文件（Annex-B 格式）
//
// 关键概念：
//   - WebRTC: 实时通信协议，用于传输音视频
//   - RTP: 实时传输协议，WebRTC 使用它来传输音视频数据包
//   - H.264: 一种视频编码格式，将视频压缩后传输
//   - NAL Unit: H.264 视频的基本单元，每个单元包含一帧或一部分帧的数据
//   - Annex-B: H.264 的一种存储格式，使用起始码（0x00000001）分隔 NAL units
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func main() {
	// ========== 第一步：解析命令行参数 ==========
	// 这些参数让用户可以自定义程序行为
	outputFile := flag.String("output", "received.h264", "输出视频文件名（H.264 格式）")
	localIP := flag.String("ip", "", "本地 IP 地址（例如：192.168.100.2）。如果不指定，自动检测")
	answerFile := flag.String("answer-file", "", "写入 answer 的文件路径（可选，如果不指定则输出到 stdout）")
	maxDuration := flag.Duration("max-duration", 0, "最大录制时长（例如：30s、5m）。0 表示无限制")
	maxSize := flag.Int64("max-size", 0, "最大文件大小（MB）。0 表示无限制")
	flag.Parse()

	// ========== 第二步：配置 WebRTC 设置引擎 ==========
	// SettingEngine 用于配置 WebRTC 的各种参数
	settingEngine := webrtc.SettingEngine{}
	// 使用公共函数配置 SettingEngine（避免重复代码）
	// Client 使用端口范围 50100-50200，与 Server 的 50000-50100 不同，避免冲突
	setupWebRTCSettingEngine(&settingEngine, *localIP, 50100, 50200)

	// ========== 第三步：准备 WebRTC 配置 ==========
	// 对于本地测试，不需要 STUN 服务器
	// STUN 服务器用于在公网上发现本机的公网 IP，但在局域网或本地测试时不需要
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// 空列表 - 只使用主机候选（host candidates），即本机的 IP 地址
		},
	}

	// ========== 第四步：创建 WebRTC API 和 PeerConnection ==========
	// API 是 WebRTC 的入口，PeerConnection 代表一个对等连接
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	// defer 确保程序退出时关闭连接，释放资源
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", cErr)
		}
	}()

	// ========== 第五步：设置事件处理器 ==========
	// 当收到远程视频流时触发
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		// Track 代表一个媒体流（视频或音频）
		// 这里我们只处理视频流
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			// 启动一个 goroutine（轻量级线程）定期发送 PLI（Picture Loss Indication）
			// PLI 是 RTCP 协议中的一种控制消息，用于请求服务器发送关键帧（I 帧）
			// 关键帧是完整的视频帧，不依赖其他帧，用于恢复视频播放
			// 每 3 秒发送一次，确保即使网络丢包也能恢复
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				defer ticker.Stop()
				for range ticker.C {
					// 检查连接是否已关闭
					if peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
						return
					}
					// 发送 PLI 请求
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if rtcpSendErr != nil {
						// 如果连接已关闭，停止发送
						if strings.Contains(rtcpSendErr.Error(), "closed") {
							return
						}
						// 只记录非关闭错误
						fmt.Fprintf(os.Stderr, "Error sending RTCP PLI: %v\n", rtcpSendErr)
					}
				}
			}()
		}

		// 获取编解码器名称（比如 "h264"）
		// MimeType 格式是 "video/h264"，我们只需要 "h264" 这部分
		codecName := strings.ToLower(strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1])
		fmt.Fprintf(os.Stderr, "Track has started, of type %d: %s \n", track.PayloadType(), codecName)

		// 只处理 H.264 视频
		if codecName == "h264" {
			// 将 H.264 数据写入文件
			// 默认帧率 30 fps，sessionDir 为空（基础 client 不使用）
			frameRate := 30.0
			writeH264ToFile(track, *outputFile, *maxDuration, *maxSize, "", frameRate)
		} else {
			fmt.Fprintf(os.Stderr, "Unsupported codec: %s, only H264 is supported\n", codecName)
		}
	})

	// 使用公共函数设置事件处理器（避免重复代码）
	setupPeerConnectionHandlers(peerConnection, nil, nil, nil)

	// ========== 第六步：读取 Server 发送的 Offer ==========
	// Offer 是 Server 发送的会话描述，包含了 Server 支持的编解码器、网络地址等信息
	// 我们从 stdin 读取（通常是通过管道或重定向传入）
	offer := webrtc.SessionDescription{}
	offerStr := readUntilNewline() // 使用公共函数
	decode(offerStr, &offer)       // 使用公共函数解码

	// ========== 第七步：设置远程会话描述 ==========
	// 告诉 PeerConnection Server 的配置信息
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// ========== 第八步：创建 Answer（应答） ==========
	// Answer 是 Client 对 Offer 的回应，包含 Client 支持的编解码器和网络地址
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// ========== 第九步：等待 ICE 候选收集完成 ==========
	// ICE 候选是 WebRTC 发现的可能用于建立连接的网络地址
	// 我们需要等待所有候选收集完成，才能生成完整的 Answer
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// 设置本地会话描述，这会启动 UDP 监听器，开始收集 ICE 候选
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	// 阻塞直到 ICE 候选收集完成
	// 这确保了 Answer 中包含所有可用的网络地址信息
	<-gatherComplete

	// ========== 第十步：输出 Answer ==========
	// 将 Answer 编码为 base64 字符串，发送回 Server
	answerStr := encode(peerConnection.LocalDescription()) // 使用公共函数
	if *answerFile != "" {
		// 写入文件（用于自动化脚本）
		err := os.WriteFile(*answerFile, []byte(answerStr+"\n"), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing answer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Answer written to file: %s (%d bytes)\n", *answerFile, len(answerStr))
	} else {
		// 输出到 stdout（用于手动复制粘贴）
		fmt.Println(answerStr)
	}

	// ========== 第十一步：保持程序运行 ==========
	// 程序需要一直运行，才能持续接收视频数据
	// select {} 会永远阻塞，直到程序被外部中断（Ctrl+C）
	select {}
}
