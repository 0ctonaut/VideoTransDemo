// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// h264_writer.go - 公共的 H.264 RTP → Annex-B 文件写入逻辑
//
// 说明：
//   - 原先实现位于 client.go 中，这里抽取为单独文件，便于 client 与 client-gcc 复用。

package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

// writeH264ToFile 接收 WebRTC 视频流，解析 RTP 数据包，提取 H.264 视频数据并写入文件
//
// 参数：
//   - track: WebRTC 远程视频轨道，用于读取 RTP 数据包
//   - filename: 输出文件名
//   - maxDuration: 最大录制时长（0 表示无限制）
//   - maxSizeMB: 最大文件大小（MB，0 表示无限制）
//   - sessionDir: Session 目录，用于读取 frame_metadata.csv 和写入 client_metrics.csv
//   - frameRate: 帧率（用于计算 stall 阈值）
func writeH264ToFile(track *webrtc.TrackRemote, filename string, maxDuration time.Duration, maxSizeMB int64, sessionDir string, frameRate float64) {
	file, err := os.Create(filename)
	if err != nil {
		panic(fmt.Sprintf("Failed to create output file: %v", err))
	}
	defer file.Close()

	writer := bufio.NewWriterSize(file, 64*1024)
	defer writer.Flush()

	packetCount := 0
	bytesWritten := int64(0)
	lastFlushTime := time.Now()
	startTime := time.Now()
	maxSizeBytes := maxSizeMB * 1024 * 1024

	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	var fuBuffer []byte
	var fuNALType byte

	fmt.Fprintf(os.Stderr, "Writing H264 stream to %s...\n", filename)
	fmt.Fprintf(os.Stderr, "Parsing RTP payload and adding Annex-B start codes\n")
	if maxDuration > 0 {
		fmt.Fprintf(os.Stderr, "Max duration: %v\n", maxDuration)
	}
	if maxSizeMB > 0 {
		fmt.Fprintf(os.Stderr, "Max size: %d MB\n", maxSizeMB)
	}

	lastReadTime := time.Now()
	readTimeout := 5 * time.Second

	// 读取 server 的开始时间（如果存在），用于统一时间基准
	var serverStartTime time.Time
	if sessionDir != "" {
		startTimePath := filepath.Join(sessionDir, "start_time.txt")
		if data, err := os.ReadFile(startTimePath); err == nil {
			if startTimeMs, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				serverStartTime = time.Unix(0, startTimeMs*int64(time.Millisecond))
				fmt.Fprintf(os.Stderr, "Loaded server start time from %s: %d ms\n", startTimePath, startTimeMs)
			} else {
				fmt.Fprintf(os.Stderr, "Warning: Failed to parse start_time.txt: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Could not read start_time.txt: %v (will use client start time)\n", err)
		}
	}

	// 读取 server 的 frame metadata（如果存在）
	var frameMetadataMap map[int]FrameMetadata
	if sessionDir != "" {
		metadataPath := filepath.Join(sessionDir, "frame_metadata.csv")
		if metadata, err := loadFrameMetadata(metadataPath); err == nil {
			frameMetadataMap = metadata
			fmt.Fprintf(os.Stderr, "Loaded %d frame metadata entries from %s\n", len(frameMetadataMap), metadataPath)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Could not load frame metadata: %v\n", err)
		}
	}

	// 创建 metrics CSV writer（如果 sessionDir 存在）
	// 如果 server 开始时间可用，使用它作为基准；否则使用 client 开始时间
	var metricsWriter *MetricsCSVWriter
	if sessionDir != "" {
		csvPath := filepath.Join(sessionDir, "client_metrics.csv")
		var err error
		if !serverStartTime.IsZero() {
			// 使用 server 的开始时间作为基准
			metricsWriter, err = NewMetricsCSVWriterWithStartTime(csvPath, serverStartTime)
		} else {
			// 使用 client 的开始时间作为基准
			metricsWriter, err = NewMetricsCSVWriter(csvPath)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create metrics CSV writer: %v\n", err)
		} else {
			defer metricsWriter.Close()
		}
	}

	// 帧检测和指标计算相关变量
	frameID := 0
	var lastFrameReceiveTime time.Time
	normalFrameInterval := time.Duration(0)
	if frameRate > 0 {
		normalFrameInterval = time.Duration(float64(time.Second) / frameRate)
	}
	stallThreshold := normalFrameInterval * 2 // 2倍正常帧间隔

	// 有效码率计算：滑动窗口（最近1秒）
	var bitWindow []BitSample
	windowDuration := 1 * time.Second
	var lastFrameBytesWritten int64 = 0
	var lastEffectiveBitrateKbps float64 = 0 // 保存上一帧的码率，用于处理异常值

	writeNALUnit := func(nalData []byte) error {
		if len(nalData) == 0 {
			return nil
		}
		if _, err := writer.Write(startCode); err != nil {
			return err
		}
		n, err := writer.Write(nalData)
		if err != nil {
			return err
		}
		bytesWritten += int64(len(startCode) + n)
		return nil
	}

	for {
		if maxDuration > 0 && time.Since(startTime) >= maxDuration {
			fmt.Fprintf(os.Stderr, "Max duration (%v) reached, stopping...\n", maxDuration)
			break
		}

		if maxSizeMB > 0 && bytesWritten >= maxSizeBytes {
			fmt.Fprintf(os.Stderr, "Max size (%d MB) reached, stopping...\n", maxSizeMB)
			break
		}

		if time.Since(lastReadTime) > readTimeout {
			fmt.Fprintf(os.Stderr, "Read timeout (%v) - no data received, assuming connection closed\n", readTimeout)
			break
		}

		rtpPacket, _, readErr := track.ReadRTP()
		if readErr != nil {
			if readErr == io.EOF {
				fmt.Fprintf(os.Stderr, "Track ended (EOF)\n")
				break
			}
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

		lastReadTime = time.Now()
		packetCount++

		payload := rtpPacket.Payload
		if len(payload) < 1 {
			continue
		}

		nalHeader := payload[0]
		nalType := nalHeader & 0x1F

		// 检测帧边界：NAL type 1 (非IDR) 或 5 (IDR) 表示新帧开始
		isFrameStart := false
		if nalType == 1 || nalType == 5 {
			isFrameStart = true
		}

		switch {
		case nalType >= 1 && nalType <= 23:
			if err := writeNALUnit(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
				continue
			}
			// 如果是帧开始，记录帧指标
			if isFrameStart {
				bitWindow, _ = recordFrameMetrics(&frameID, &lastFrameReceiveTime, normalFrameInterval, stallThreshold,
					frameMetadataMap, bitWindow, windowDuration, metricsWriter, bytesWritten, &lastFrameBytesWritten, serverStartTime, &lastEffectiveBitrateKbps)
			}
			fuBuffer = nil

		case nalType == 24:
			offset := 1
			for offset < len(payload) {
				if offset+2 > len(payload) {
					break
				}
				nalSize := int(payload[offset])<<8 | int(payload[offset+1])
				offset += 2
				if offset+nalSize > len(payload) {
					break
				}
				nalData := payload[offset : offset+nalSize]
				if err := writeNALUnit(nalData); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing STAP-A NAL unit: %v\n", err)
					break
				}
				offset += nalSize
			}
			fuBuffer = nil

		case nalType == 28:
			if len(payload) < 2 {
				continue
			}
			fuHeader := payload[1]
			start := (fuHeader & 0x80) != 0
			end := (fuHeader & 0x40) != 0
			actualNALType := fuHeader & 0x1F

			if start {
				fuNALType = actualNALType
				fuBuffer = []byte{(nalHeader & 0xE0) | actualNALType}
				fuBuffer = append(fuBuffer, payload[2:]...)
			} else {
				if fuBuffer != nil && (fuHeader&0x1F) == fuNALType {
					fuBuffer = append(fuBuffer, payload[2:]...)
				} else {
					fuBuffer = nil
					continue
				}
			}

			if end {
				if fuBuffer != nil {
					if err := writeNALUnit(fuBuffer); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing FU-A NAL unit: %v\n", err)
					}
					// FU-A 结束表示完整 NAL 单元，检查是否是帧开始
					if fuNALType == 1 || fuNALType == 5 {
						bitWindow, _ = recordFrameMetrics(&frameID, &lastFrameReceiveTime, normalFrameInterval, stallThreshold,
							frameMetadataMap, bitWindow, windowDuration, metricsWriter, bytesWritten, &lastFrameBytesWritten, serverStartTime, &lastEffectiveBitrateKbps)
					}
					fuBuffer = nil
				}
			}

		default:
			fmt.Fprintf(os.Stderr, "Warning: Unsupported NAL type %d, skipping\n", nalType)
		}

		if time.Since(lastFlushTime) > 1*time.Second {
			writer.Flush()
			file.Sync()
			elapsed := time.Since(startTime)
			sizeMB := float64(bytesWritten) / (1024 * 1024)
			fmt.Fprintf(os.Stderr, "Progress: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed.Round(time.Second))
			lastFlushTime = time.Now()
		}
	}

	if fuBuffer != nil {
		fmt.Fprintf(os.Stderr, "Warning: Discarding incomplete FU-A fragment\n")
	}

	writer.Flush()
	file.Sync()
	elapsed := time.Since(startTime)
	sizeMB := float64(bytesWritten) / (1024 * 1024)
	fmt.Fprintf(os.Stderr, "Completed: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed)
	fmt.Fprintf(os.Stderr, "File flushed and synced to disk\n")
	fmt.Fprintf(os.Stderr, "You can now use FFmpeg to process this file:\n")
	fmt.Fprintf(os.Stderr, "  ffmpeg -fflags +genpts -r 30 -i %s -c:v copy received.mp4\n", filename)
}

// loadFrameMetadata 从 CSV 文件加载帧元数据
func loadFrameMetadata(csvPath string) (map[int]FrameMetadata, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	metadataMap := make(map[int]FrameMetadata)
	for i, record := range records {
		if i == 0 {
			continue // Skip header
		}
		if len(record) < 4 {
			continue
		}

		frameID, err := strconv.Atoi(record[0])
		if err != nil {
			continue
		}
		sendStartMs, err := strconv.ParseInt(record[1], 10, 64)
		if err != nil {
			continue
		}
		sendEndMs, err := strconv.ParseInt(record[2], 10, 64)
		if err != nil {
			continue
		}
		frameBits, err := strconv.Atoi(record[3])
		if err != nil {
			continue
		}

		// 保存相对时间戳（毫秒），用于端到端延迟计算
		metadataMap[frameID] = FrameMetadata{
			FrameID:     frameID,
			SendStart:   time.Unix(0, sendStartMs*int64(time.Millisecond)), // 保留用于兼容
			SendEnd:     time.Unix(0, sendEndMs*int64(time.Millisecond)),   // 保留用于兼容
			FrameBits:   frameBits,
			SendStartMs: sendStartMs, // 相对时间戳（毫秒）
			SendEndMs:   sendEndMs,   // 相对时间戳（毫秒）
		}
	}

	return metadataMap, nil
}

// BitSample 用于有效码率计算的滑动窗口样本
type BitSample struct {
	Time  time.Time
	Bits  int64
}

// recordFrameMetrics 记录一帧的指标（延迟、stall、有效码率）
// 返回更新后的 bitWindow 和计算出的 effectiveBitrateKbps
func recordFrameMetrics(frameID *int, lastFrameReceiveTime *time.Time,
	normalFrameInterval time.Duration, stallThreshold time.Duration,
	frameMetadataMap map[int]FrameMetadata, bitWindow []BitSample, windowDuration time.Duration,
	metricsWriter *MetricsCSVWriter, currentBytesWritten int64, lastFrameBytesWritten *int64, serverStartTime time.Time,
	lastEffectiveBitrateKbps *float64) ([]BitSample, float64) {

	receiveTime := time.Now()
	*frameID++

	// 计算端到端延迟（如果 server metadata 存在）
	// 现在 server 和 client 使用统一的时间基准（server 的开始时间），可以计算端到端延迟
	var e2eLatencyMs float64
	if metadata, ok := frameMetadataMap[*frameID]; ok && !serverStartTime.IsZero() {
		// metadata.SendStartMs 是 server 的相对时间戳（毫秒，从 server 开始时间算起）
		// receiveTime 是 client 的绝对时间，需要转换为相对于 server 开始时间的相对时间戳（毫秒）
		clientRelativeMs := receiveTime.Sub(serverStartTime).Milliseconds()
		// 端到端延迟 = client相对时间 - server相对时间
		e2eLatencyMs = float64(clientRelativeMs - metadata.SendStartMs)
	}

	// 计算帧间隔延迟
	var interFrameLatencyMs float64
	var stall bool
	if !lastFrameReceiveTime.IsZero() {
		interFrameLatency := receiveTime.Sub(*lastFrameReceiveTime)
		interFrameLatencyMs = float64(interFrameLatency.Nanoseconds()) / 1e6
		// 检测 stall：帧间隔 > 2倍正常帧间隔
		if stallThreshold > 0 && interFrameLatency > stallThreshold {
			stall = true
		}
	} else {
		// 第一帧，使用端到端延迟作为帧间隔延迟
		interFrameLatencyMs = e2eLatencyMs
	}

	// 使用端到端延迟作为主要延迟指标（如果可用），否则使用帧间隔延迟
	latencyMs := e2eLatencyMs
	if latencyMs == 0 && interFrameLatencyMs > 0 {
		latencyMs = interFrameLatencyMs
	} else if latencyMs == 0 {
		// 第一帧且没有端到端延迟，使用帧间隔延迟
		latencyMs = interFrameLatencyMs
	}

	// 更新有效码率滑动窗口
	// 计算当前帧的比特数（当前总字节数 - 上次总字节数）
	frameBits := (currentBytesWritten - *lastFrameBytesWritten) * 8
	if frameBits < 0 {
		frameBits = 0 // 防止负数
	}
	bitWindow = append(bitWindow, BitSample{
		Time: receiveTime,
		Bits: frameBits,
	})
	*lastFrameBytesWritten = currentBytesWritten

	// 移除窗口外的样本
	cutoffTime := receiveTime.Add(-windowDuration)
	validStart := 0
	for i, sample := range bitWindow {
		if sample.Time.After(cutoffTime) {
			validStart = i
			break
		}
	}
	bitWindow = bitWindow[validStart:]

	// 计算有效码率（窗口内的总比特数 / 窗口时长）
	var effectiveBitrateKbps float64
	if len(bitWindow) >= 2 {
		windowStart := bitWindow[0].Time
		windowEnd := bitWindow[len(bitWindow)-1].Time
		windowDurationSec := windowEnd.Sub(windowStart).Seconds()
		
		// 检查窗口是否足够大：至少 10ms 或至少 5 帧
		minWindowDuration := 10 * time.Millisecond
		minWindowFrames := 5
		
		if windowDurationSec > 0 && 
		   windowDurationSec >= minWindowDuration.Seconds() && 
		   len(bitWindow) >= minWindowFrames {
			// 累加窗口内所有帧的比特数
			var totalBits int64
			for _, sample := range bitWindow {
				totalBits += sample.Bits
			}
			if totalBits > 0 {
				effectiveBitrateKbps = float64(totalBits) / windowDurationSec / 1000.0
			}
		}
		
		// 如果窗口太小或计算出的码率异常高（> 1000 Mbps），使用上一帧的码率
		if effectiveBitrateKbps == 0 || effectiveBitrateKbps > 1000000 {
			if *lastEffectiveBitrateKbps > 0 {
				effectiveBitrateKbps = *lastEffectiveBitrateKbps
			} else {
				// 第一帧或没有上一帧码率，设为 0
				effectiveBitrateKbps = 0
			}
		}
	} else {
		// 窗口太小（少于 2 帧），使用上一帧的码率或设为 0
		if *lastEffectiveBitrateKbps > 0 {
			effectiveBitrateKbps = *lastEffectiveBitrateKbps
		} else {
			effectiveBitrateKbps = 0
		}
	}
	
	// 更新上一帧的码率
	*lastEffectiveBitrateKbps = effectiveBitrateKbps

	// 写入 metrics CSV
	if metricsWriter != nil {
		metricsWriter.WriteMetric(FrameMetric{
			Timestamp:            receiveTime,
			FrameIndex:           *frameID,
			LatencyMillis:        latencyMs,
			Stall:                stall,
			EffectiveBitrateKbps: effectiveBitrateKbps,
		})
	}

	*lastFrameReceiveTime = receiveTime
	return bitWindow, effectiveBitrateKbps
}




