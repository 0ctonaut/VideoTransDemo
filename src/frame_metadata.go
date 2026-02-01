// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// frame_metadata.go - Server 端帧发送元数据记录工具
//
// 说明：
//   - 用于记录每帧的发送时间戳，供 client 端计算端到端延迟
//   - 所有 server（GCC、NDTC、Salsify、BurstRTC）可以复用此工具

package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FrameMetadata 表示一帧的发送元数据
type FrameMetadata struct {
	FrameID     int
	SendStart   time.Time
	SendEnd     time.Time
	FrameBits   int
	SendStartMs int64 // 相对时间戳（毫秒），用于端到端延迟计算
	SendEndMs   int64 // 相对时间戳（毫秒）
}

// FrameMetadataWriter 是一个线程安全的 CSV 写入器，用于记录帧发送元数据
type FrameMetadataWriter struct {
	mu        sync.Mutex
	writer    *csv.Writer
	file      *os.File
	startTime time.Time // 记录开始时间，用于计算相对时间戳
}

// NewFrameMetadataWriter 创建一个新的帧元数据 CSV 写入器
func NewFrameMetadataWriter(csvPath string) (*FrameMetadataWriter, error) {
	if csvPath == "" {
		return nil, fmt.Errorf("csvPath is empty")
	}

	dir := filepath.Dir(csvPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create metadata directory: %w", err)
	}

	f, err := os.Create(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata csv: %w", err)
	}

	w := csv.NewWriter(f)

	header := []string{
		"frame_id",
		"send_start_ms", // 相对时间戳（毫秒，从开始时间算起）
		"send_end_ms",   // 相对时间戳（毫秒，从开始时间算起）
		"frame_bits",
	}
	if err = w.Write(header); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to write metadata header: %w", err)
	}
	w.Flush()

	startTime := time.Now()

	// 将开始时间（Unix 时间戳，毫秒）写入文件，供 client 端使用
	startTimeFile := filepath.Join(dir, "start_time.txt")
	if err := os.WriteFile(startTimeFile, []byte(fmt.Sprintf("%d\n", startTime.UnixMilli())), 0o644); err != nil {
		// 如果写入失败，只打印警告，不影响主流程
		fmt.Fprintf(os.Stderr, "Warning: Failed to write start_time.txt: %v\n", err)
	}

	return &FrameMetadataWriter{
		writer:    w,
		file:      f,
		startTime: startTime,
	}, nil
}

// WriteMetadata 写入一条帧元数据，不会在出错时 panic，只打印错误日志
func (m *FrameMetadataWriter) WriteMetadata(metadata FrameMetadata) {
	if m == nil || m.writer == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 计算相对时间戳（从开始时间算起的毫秒数）
	startMs := metadata.SendStart.Sub(m.startTime).Milliseconds()
	endMs := metadata.SendEnd.Sub(m.startTime).Milliseconds()

	record := []string{
		fmt.Sprintf("%d", metadata.FrameID),
		fmt.Sprintf("%d", startMs),
		fmt.Sprintf("%d", endMs),
		fmt.Sprintf("%d", metadata.FrameBits),
	}
	if err := m.writer.Write(record); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing frame metadata CSV: %v\n", err)
		return
	}
	m.writer.Flush()
}

// Close 关闭底层文件句柄
func (m *FrameMetadataWriter) Close() {
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
			fmt.Fprintf(os.Stderr, "Error closing frame metadata CSV file: %v\n", err)
		}
	}
}
