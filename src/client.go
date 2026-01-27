// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

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
	"bufio"
	"flag"
	"fmt"
	"io"
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
			writeH264ToFile(track, *outputFile, *maxDuration, *maxSize)
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

// writeH264ToFile 接收 WebRTC 视频流，解析 RTP 数据包，提取 H.264 视频数据并写入文件
//
// 这个函数是整个客户端最复杂的部分，需要理解以下概念：
//
// 1. RTP（Real-time Transport Protocol）实时传输协议
//   - WebRTC 使用 RTP 来传输音视频数据包
//   - 每个 RTP 包包含一个 RTP 头（12 字节）和 payload（实际数据）
//   - track.ReadRTP() 已经帮我们解析了 RTP 头，我们只需要处理 payload
//
// 2. H.264 NAL Unit（Network Abstraction Layer Unit）
//   - H.264 视频被分割成多个 NAL units，每个 unit 包含一帧或一部分帧的数据
//   - NAL unit 的类型有很多种：
//   - SPS（Sequence Parameter Set，序列参数集）：包含视频的宽高、帧率等信息
//   - PPS（Picture Parameter Set，图像参数集）：包含编码参数
//   - IDR 帧（关键帧）：完整的视频帧，可以独立解码
//   - 普通帧：依赖其他帧才能解码
//
// 3. RTP 对 H.264 的封装方式（RFC 6184）
//   - Single NAL Unit（类型 1-23）：一个 RTP 包包含一个完整的 NAL unit
//   - STAP-A（类型 24）：一个 RTP 包包含多个小的 NAL units（聚合）
//   - FU-A（类型 28）：一个大的 NAL unit 被分割成多个 RTP 包（分片）
//
// 4. Annex-B 格式
//   - 这是 H.264 的一种存储格式，每个 NAL unit 前面有一个起始码
//   - 起始码是固定的 4 字节：0x00 0x00 0x00 0x01
//   - FFmpeg 等工具需要这种格式才能正确解析视频
//
// 参数：
//   - track: WebRTC 远程视频轨道，用于读取 RTP 数据包
//   - filename: 输出文件名
//   - maxDuration: 最大录制时长（0 表示无限制）
//   - maxSizeMB: 最大文件大小（MB，0 表示无限制）
func writeH264ToFile(track *webrtc.TrackRemote, filename string, maxDuration time.Duration, maxSizeMB int64) {
	// ========== 初始化文件写入 ==========
	file, err := os.Create(filename)
	if err != nil {
		panic(fmt.Sprintf("Failed to create output file: %v", err))
	}
	defer file.Close()

	// 使用带缓冲的写入器，提高写入性能
	// 64KB 的缓冲区：数据先写入内存缓冲区，缓冲区满了或调用 Flush() 时才真正写入磁盘
	writer := bufio.NewWriterSize(file, 64*1024)
	defer writer.Flush() // 程序退出时确保所有数据都写入磁盘

	// ========== 初始化统计变量 ==========
	packetCount := 0                        // 接收到的 RTP 包数量
	bytesWritten := int64(0)                // 写入文件的字节数
	lastFlushTime := time.Now()             // 上次刷新缓冲区的时间
	startTime := time.Now()                 // 开始时间
	maxSizeBytes := maxSizeMB * 1024 * 1024 // 转换为字节

	// Annex-B 起始码：每个 NAL unit 前面都需要这个
	// 0x00 0x00 0x00 0x01 是 H.264 Annex-B 格式的标准起始码
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// ========== FU-A 分片重组缓冲区 ==========
	// 当一个大的 NAL unit 被分割成多个 RTP 包时，需要先收集所有分片，再重组
	var fuBuffer []byte // 存储正在重组的 NAL unit 数据
	var fuNALType byte  // 当前重组的 NAL unit 类型（用于验证分片是否匹配）

	fmt.Fprintf(os.Stderr, "Writing H264 stream to %s...\n", filename)
	fmt.Fprintf(os.Stderr, "Parsing RTP payload and adding Annex-B start codes\n")
	if maxDuration > 0 {
		fmt.Fprintf(os.Stderr, "Max duration: %v\n", maxDuration)
	}
	if maxSizeMB > 0 {
		fmt.Fprintf(os.Stderr, "Max size: %d MB\n", maxSizeMB)
	}

	// ========== 超时检测 ==========
	// 如果长时间没有收到数据，认为连接已断开
	lastReadTime := time.Now()
	readTimeout := 5 * time.Second

	// ========== 辅助函数：写入一个完整的 NAL unit ==========
	// 这个函数会在 NAL unit 前面添加起始码，然后写入文件
	writeNALUnit := func(nalData []byte) error {
		if len(nalData) == 0 {
			return nil
		}
		// 写入起始码（0x00 0x00 0x00 0x01）
		if _, err := writer.Write(startCode); err != nil {
			return err
		}
		// 写入 NAL unit 的实际数据
		n, err := writer.Write(nalData)
		if err != nil {
			return err
		}
		// 更新写入字节数统计（包括起始码）
		bytesWritten += int64(len(startCode) + n)
		return nil
	}

	// ========== 主循环：持续接收和解析 RTP 数据包 ==========
	for {
		// 检查是否达到最大录制时长
		if maxDuration > 0 && time.Since(startTime) >= maxDuration {
			fmt.Fprintf(os.Stderr, "Max duration (%v) reached, stopping...\n", maxDuration)
			break
		}

		// 检查是否达到最大文件大小
		if maxSizeMB > 0 && bytesWritten >= maxSizeBytes {
			fmt.Fprintf(os.Stderr, "Max size (%d MB) reached, stopping...\n", maxSizeMB)
			break
		}

		// 检查读取超时：如果 5 秒内没有收到数据，认为连接已断开
		if time.Since(lastReadTime) > readTimeout {
			fmt.Fprintf(os.Stderr, "Read timeout (%v) - no data received, assuming connection closed\n", readTimeout)
			break
		}

		// ========== 读取 RTP 数据包 ==========
		// ReadRTP() 返回完整的 RTP 包，包括 RTP 头和 payload
		// WebRTC 库已经帮我们处理了 RTP 头的解析，我们只需要处理 payload
		rtpPacket, _, readErr := track.ReadRTP()
		if readErr != nil {
			if readErr == io.EOF {
				fmt.Fprintf(os.Stderr, "Track ended (EOF)\n")
				break
			}
			// 检查是否是连接关闭错误
			if strings.Contains(readErr.Error(), "closed") || strings.Contains(readErr.Error(), "EOF") {
				fmt.Fprintf(os.Stderr, "Connection closed: %v\n", readErr)
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading track: %v\n", readErr)
			break
		}

		if rtpPacket == nil {
			continue
		}

		lastReadTime = time.Now() // 更新最后读取时间
		packetCount++

		// ========== 提取 RTP payload ==========
		// payload 就是实际的 H.264 数据（但可能被 RTP 封装了）
		payload := rtpPacket.Payload
		if len(payload) < 1 {
			continue
		}

		// ========== 解析 RTP H.264 payload（根据 RFC 6184 标准） ==========
		// payload 的第一个字节是 NAL unit header
		// 低 5 位（bit 0-4）表示 NAL unit 类型
		nalHeader := payload[0]
		nalType := nalHeader & 0x1F // 0x1F = 00011111，提取低 5 位

		// 根据 NAL unit 类型，采用不同的处理方式
		switch {
		case nalType >= 1 && nalType <= 23:
			// ========== 情况 1：Single NAL Unit（单个 NAL unit） ==========
			// 这是最简单的情况：一个 RTP 包包含一个完整的 NAL unit
			// 类型 1-23 都是这种情况
			// 处理方式：直接提取 payload，添加起始码，写入文件
			if err := writeNALUnit(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
				continue
			}
			// 清除任何待处理的 FU 缓冲区（因为收到了完整的 NAL unit）
			fuBuffer = nil

		case nalType == 24:
			// ========== 情况 2：STAP-A（Single-time Aggregation Packet，聚合包） ==========
			// 一个 RTP 包包含多个小的 NAL units（为了节省网络开销）
			// 格式：[STAP-A header] + [2字节大小] + [NAL unit 1] + [2字节大小] + [NAL unit 2] + ...
			// 处理方式：逐个提取每个 NAL unit，分别添加起始码，写入文件
			offset := 1 // 跳过 STAP-A header（第一个字节）
			for offset < len(payload) {
				// 检查是否有足够的数据读取大小字段（2 字节）
				if offset+2 > len(payload) {
					break
				}
				// 读取 NAL unit 的大小（大端序，2 字节）
				nalSize := int(payload[offset])<<8 | int(payload[offset+1])
				offset += 2
				// 检查是否有足够的数据读取完整的 NAL unit
				if offset+nalSize > len(payload) {
					break
				}
				// 提取 NAL unit 数据
				nalData := payload[offset : offset+nalSize]
				// 写入文件（会自动添加起始码）
				if err := writeNALUnit(nalData); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing STAP-A NAL unit: %v\n", err)
					break
				}
				offset += nalSize
			}
			fuBuffer = nil

		case nalType == 28:
			// ========== 情况 3：FU-A（Fragmentation Unit，分片单元） ==========
			// 一个大的 NAL unit 被分割成多个 RTP 包（因为 RTP 包有大小限制）
			// 格式：[FU indicator] + [FU header] + [FU payload]
			// FU header 的 bit 7 表示是否是第一个分片（start）
			// FU header 的 bit 6 表示是否是最后一个分片（end）
			// FU header 的低 5 位是实际的 NAL unit 类型
			// 处理方式：收集所有分片，重组完整的 NAL unit，然后写入文件
			if len(payload) < 2 {
				continue
			}
			fuHeader := payload[1]
			start := (fuHeader & 0x80) != 0  // bit 7：是否是第一个分片
			end := (fuHeader & 0x40) != 0    // bit 6：是否是最后一个分片
			actualNALType := fuHeader & 0x1F // 低 5 位：实际的 NAL unit 类型

			if start {
				// 第一个分片：初始化缓冲区
				fuNALType = actualNALType
				// 重建 NAL unit header：保留原 header 的高 3 位（F、NRI），替换低 5 位为实际类型
				fuBuffer = []byte{(nalHeader & 0xE0) | actualNALType}
				// 添加第一个分片的 payload（跳过 FU indicator 和 FU header）
				fuBuffer = append(fuBuffer, payload[2:]...)
			} else {
				// 中间或最后一个分片：追加到缓冲区
				if fuBuffer != nil && (fuHeader&0x1F) == fuNALType {
					// 验证类型匹配（防止分片混乱）
					fuBuffer = append(fuBuffer, payload[2:]...)
				} else {
					// 类型不匹配，丢弃（可能是网络乱序或丢包）
					fuBuffer = nil
					continue
				}
			}

			if end {
				// 最后一个分片：重组完成，写入文件
				if fuBuffer != nil {
					if err := writeNALUnit(fuBuffer); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing FU-A NAL unit: %v\n", err)
					}
					fuBuffer = nil
				}
			}

		default:
			// 未知或不支持的 NAL 类型，跳过
			fmt.Fprintf(os.Stderr, "Warning: Unsupported NAL type %d, skipping\n", nalType)
		}

		// ========== 定期刷新缓冲区 ==========
		// 每 1 秒刷新一次缓冲区，确保数据及时写入磁盘
		// 这样即使程序崩溃，最多也只丢失 1 秒的数据
		if time.Since(lastFlushTime) > 1*time.Second {
			writer.Flush() // 将缓冲区数据写入文件
			file.Sync()    // 强制操作系统将数据写入磁盘（而不是只写入系统缓存）
			elapsed := time.Since(startTime)
			sizeMB := float64(bytesWritten) / (1024 * 1024)
			fmt.Fprintf(os.Stderr, "Progress: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed.Round(time.Second))
			lastFlushTime = time.Now()
		}
	}

	// ========== 清理工作 ==========
	// 如果还有未完成的 FU-A 分片，丢弃它（不写入不完整的 NAL unit）
	// 不完整的 NAL unit 会导致视频文件损坏，无法播放
	if fuBuffer != nil {
		fmt.Fprintf(os.Stderr, "Warning: Discarding incomplete FU-A fragment\n")
	}

	// 最终刷新：确保所有数据都写入磁盘
	writer.Flush()
	file.Sync()
	elapsed := time.Since(startTime)
	sizeMB := float64(bytesWritten) / (1024 * 1024)
	fmt.Fprintf(os.Stderr, "Completed: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed)
	fmt.Fprintf(os.Stderr, "File flushed and synced to disk\n")
	fmt.Fprintf(os.Stderr, "You can now use FFmpeg to process this file:\n")
	fmt.Fprintf(os.Stderr, "  ffmpeg -fflags +genpts -r 30 -i %s -c:v copy received.mp4\n", filename)
}

// 注意：encode、decode、readUntilNewline 函数已移至 common.go，避免代码重复
