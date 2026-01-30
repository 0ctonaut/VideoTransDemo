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
	"fmt"
	"io"
	"os"
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
func writeH264ToFile(track *webrtc.TrackRemote, filename string, maxDuration time.Duration, maxSizeMB int64) {
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

		switch {
		case nalType >= 1 && nalType <= 23:
			if err := writeNALUnit(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
				continue
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




