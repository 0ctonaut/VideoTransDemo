// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// common.go 包含 client.go 和 server.go 共用的函数
// 这些函数用于 WebRTC 的 SDP（会话描述协议）编码/解码和配置
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

// encode 将 WebRTC 的 SessionDescription（会话描述）编码为 base64 格式的 JSON 字符串
// 
// 什么是 SessionDescription？
// - 它包含了 WebRTC 连接所需的所有信息：支持的编解码器、网络地址（IP:端口）、加密密钥等
// - 在 WebRTC 中，需要先交换这些信息（通过 SDP），双方才能建立连接
//
// 为什么要 base64 编码？
// - JSON 数据可能包含特殊字符，base64 编码后可以安全地通过文本方式传输（比如复制粘贴）
// - 这是 WebRTC 标准推荐的传输方式
//
// 参数：
//   - obj: 要编码的 SessionDescription 对象（包含 offer 或 answer）
//
// 返回：
//   - base64 编码的 JSON 字符串，可以直接写入文件或通过 stdin/stdout 传输
func encode(obj *webrtc.SessionDescription) string {
	// 第一步：将 SessionDescription 对象转换为 JSON 格式
	// JSON 是一种文本格式，可以表示复杂的数据结构
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}

	// 第二步：将 JSON 字节数组进行 base64 编码
	// base64 编码可以将任意二进制数据转换为只包含字母、数字和几个特殊字符的字符串
	// 这样便于通过文本方式传输（比如复制粘贴、写入文件等）
	return base64.StdEncoding.EncodeToString(b)
}

// decode 将 base64 编码的 JSON 字符串解码为 WebRTC 的 SessionDescription 对象
//
// 这是 encode 函数的逆过程：
// base64 字符串 -> JSON 字节数组 -> SessionDescription 对象
//
// 参数：
//   - in: base64 编码的 JSON 字符串（通常是从文件或 stdin 读取的）
//   - obj: 用于存储解码结果的 SessionDescription 对象指针（会被修改）
//
// 使用示例：
//   answer := webrtc.SessionDescription{}
//   decode(answerStr, &answer)
func decode(in string, obj *webrtc.SessionDescription) {
	// 第一步：将 base64 字符串解码为原始的 JSON 字节数组
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		panic(err)
	}

	// 第二步：将 JSON 字节数组解析为 SessionDescription 对象
	// 这里会填充 obj 指向的结构体，包含所有连接信息
	if err = json.Unmarshal(b, obj); err != nil {
		panic(err)
	}
}

// readUntilNewline 从标准输入（stdin）读取一行文本，直到遇到换行符
//
// 这个函数用于交互式输入：当用户复制粘贴 SDP 字符串后，按回车键，函数就会读取这一行
//
// 返回：
//   - 读取到的文本（已去除首尾空白字符）
//   - 如果遇到错误或 EOF（文件结束），可能返回空字符串
//
// 使用场景：
//   - Client: 从 stdin 读取 server 发送的 offer
//   - Server: 从 stdin 读取 client 发送的 answer（交互模式）
func readUntilNewline() (in string) {
	var err error

	// 创建一个带缓冲的读取器，从标准输入读取
	// bufio.Reader 可以高效地读取文本，一次读取一行
	r := bufio.NewReader(os.Stdin)

	// 循环读取，直到读取到非空行或遇到错误
	for {
		// ReadString('\n') 会读取直到遇到换行符（\n）或文件结束
		// 返回的字符串包含换行符本身
		in, err = r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			panic(err)
		}

		// 去除首尾的空白字符（空格、制表符、换行符等）
		// 如果去除后还有内容，说明读取到了有效数据
		if in = strings.TrimSpace(in); len(in) > 0 {
			break
		}

		// 如果遇到文件结束（EOF）且没有读取到内容，退出循环
		if err == io.EOF {
			break
		}
		// 如果是空行，继续循环等待下一行
	}

	return
}

// setupWebRTCSettingEngine 配置 WebRTC 的 SettingEngine（设置引擎）
//
// SettingEngine 用于配置 WebRTC 的各种参数，比如：
// - UDP 端口范围：限制 WebRTC 使用的端口，便于防火墙配置
// - ICE 超时时间：控制连接建立的超时时间
// - NAT 映射：指定本地 IP 地址，用于局域网通信
//
// 参数：
//   - settingEngine: 要配置的 SettingEngine 对象（会被修改）
//   - localIP: 本地 IP 地址（可选，为空则自动检测）
//   - portRangeStart: UDP 端口范围起始值
//   - portRangeEnd: UDP 端口范围结束值
//
// 使用场景：
//   - Server 和 Client 都需要配置 SettingEngine，但端口范围可能不同（避免冲突）
func setupWebRTCSettingEngine(settingEngine *webrtc.SettingEngine, localIP string, portRangeStart, portRangeEnd uint16) {
	// 设置 UDP 端口范围
	// WebRTC 使用 UDP 协议传输音视频数据，这里限制它只能使用指定范围的端口
	// 好处：
	//   1. 便于防火墙配置（只需要开放这个端口范围）
	//   2. 便于调试（知道数据从哪些端口发送）
	//   3. 避免端口冲突（server 和 client 使用不同的范围）
	if err := settingEngine.SetEphemeralUDPPortRange(portRangeStart, portRangeEnd); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to set port range: %v\n", err)
	}

	// 设置 ICE 超时时间
	// ICE（Interactive Connectivity Establishment）是 WebRTC 用来建立连接的协议
	// 它会尝试多种方式连接（直连、通过 STUN/TURN 服务器等）
	//
	// 三个超时参数：
	//   - DisconnectedTimeout: 连接断开后，等待多久才认为连接失败（10秒）
	//   - FailedTimeout: 连接失败后，等待多久才放弃重试（30秒）
	//   - KeepaliveInterval: 发送心跳包的间隔，用于保持连接活跃（2秒）
	settingEngine.SetICETimeouts(
		10*time.Second, // 断开超时：10秒内没收到数据就认为断开
		30*time.Second, // 失败超时：30秒内无法建立连接就放弃
		2*time.Second,  // 心跳间隔：每2秒发送一次心跳包保持连接
	)

	// 配置 NAT 1-to-1 IP 映射（如果指定了 IP 地址）
	// NAT（Network Address Translation）是网络地址转换，用于局域网和公网之间的地址映射
	//
	// 为什么要指定 IP？
	// - 在局域网环境中（比如使用虚拟网卡对），需要明确告诉 WebRTC 使用哪个 IP
	// - 如果不指定，WebRTC 可能检测到多个 IP（比如 127.0.0.1、192.168.x.x），导致连接失败
	if localIP != "" {
		// 验证 IP 地址格式是否正确
		ip := net.ParseIP(localIP)
		if ip == nil {
			fmt.Fprintf(os.Stderr, "Warning: Invalid IP address: %s, using auto-detect\n", localIP)
		} else {
			// 设置 NAT 映射：告诉 WebRTC 使用这个 IP 地址作为本地地址
			// ICECandidateTypeHost 表示这是"主机候选"，即本机的真实 IP 地址
			settingEngine.SetNAT1To1IPs([]string{localIP}, webrtc.ICECandidateTypeHost)
			fmt.Fprintf(os.Stderr, "Using specified IP address: %s\n", localIP)
		}
	}
}

// setupPeerConnectionHandlers 设置 PeerConnection 的事件处理器
//
// PeerConnection 是 WebRTC 的核心对象，代表一个对等连接
// 它会在不同阶段触发各种事件，我们需要注册处理器来响应这些事件
//
// 参数：
//   - peerConnection: 要设置处理器的 PeerConnection 对象
//   - onICECandidate: ICE 候选事件处理器（可选，为 nil 则使用默认日志输出）
//   - onICEConnectionStateChange: ICE 连接状态变化处理器（可选）
//   - onConnectionStateChange: PeerConnection 状态变化处理器（可选）
//
// 事件说明：
//   - OnICECandidate: 当发现新的网络候选地址时触发（比如发现本机 IP、通过 STUN 发现的公网 IP）
//   - OnICEConnectionStateChange: 当 ICE 连接状态改变时触发（连接中、已连接、失败等）
//   - OnConnectionStateChange: 当整个 PeerConnection 状态改变时触发（更高级别的状态）
func setupPeerConnectionHandlers(
	peerConnection *webrtc.PeerConnection,
	onICECandidate func(*webrtc.ICECandidate),
	onICEConnectionStateChange func(webrtc.ICEConnectionState),
	onConnectionStateChange func(webrtc.PeerConnectionState),
) {
	// 设置 ICE 候选事件处理器
	// ICE 候选（Candidate）是 WebRTC 发现的可能用于建立连接的网络地址
	// 比如：本机 IP:端口、通过 STUN 服务器发现的公网 IP:端口 等
	if onICECandidate != nil {
		peerConnection.OnICECandidate(onICECandidate)
	} else {
		// 默认处理器：只打印日志
		peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate != nil {
				fmt.Fprintf(os.Stderr, "ICE Candidate: %s\n", candidate.String())
			} else {
				fmt.Fprintf(os.Stderr, "ICE Candidate gathering completed\n")
			}
		})
	}

	// 设置 ICE 连接状态变化处理器
	// ICE 连接状态包括：
	//   - New: 新建，刚开始
	//   - Checking: 正在检查连接
	//   - Connected: 已连接
	//   - Completed: 连接完成
	//   - Failed: 连接失败
	//   - Disconnected: 连接断开
	//   - Closed: 已关闭
	if onICEConnectionStateChange != nil {
		peerConnection.OnICEConnectionStateChange(onICEConnectionStateChange)
	} else {
		// 默认处理器：打印状态变化
		peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
			if connectionState == webrtc.ICEConnectionStateFailed {
				fmt.Fprintf(os.Stderr, "ERROR: ICE connection failed!\n")
			}
		})
	}

	// 设置 PeerConnection 状态变化处理器
	// 这是比 ICE 状态更高级别的状态，包括：
	//   - New: 新建
	//   - Connecting: 连接中
	//   - Connected: 已连接
	//   - Disconnected: 断开
	//   - Failed: 失败
	//   - Closed: 已关闭
	if onConnectionStateChange != nil {
		peerConnection.OnConnectionStateChange(onConnectionStateChange)
	} else {
		// 默认处理器：打印状态变化
		peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
			if s == webrtc.PeerConnectionStateFailed {
				fmt.Fprintf(os.Stderr, "ERROR: Peer connection failed!\n")
			}
		})
	}
}

