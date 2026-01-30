// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// metrics.go - 公共指标与 CSV 写入工具
//
// 说明：
//   - 这里定义了一些在 GCC / NDTC / Salsify 等算法之间可以复用的
//     基本数据结构与工具函数，方便后续扩展。
//   - 当前版本中，核心 WebRTC 统计数据的采集还没有打通，
//     这些类型主要作为预留扩展点，便于后续实现。

package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"sync"
	"time"
)

// FrameMetric 表示单帧的关键统计信息
//   - 用于后续在不同拥塞控制算法之间对比：
//     * Average & P99 frame latency
//     * Stall rate
//     * Effective bitrate
type FrameMetric struct {
	Timestamp            time.Time
	FrameIndex           int
	LatencyMillis        float64
	Stall                bool
	EffectiveBitrateKbps float64
}

// MetricsCSVWriter 是一个简单的线程安全 CSV 写入器
//   - 目前只在 GCC / NDTC / Salsify 预留入口时使用
//   - 每个 session 建议创建一个实例，将 CSV 保存在 session 目录下
type MetricsCSVWriter struct {
	mu     sync.Mutex
	writer *csv.Writer
	file   *os.File
}

// NewMetricsCSVWriter 创建一个新的 CSV 写入器。
// 如果创建失败，返回 nil 和错误，调用方可以选择忽略指标写入。
func NewMetricsCSVWriter(csvPath string) (*MetricsCSVWriter, error) {
	if csvPath == "" {
		return nil, fmt.Errorf("csvPath is empty")
	}

	if err := os.MkdirAll(filepathDir(csvPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create metrics directory: %w", err)
	}

	f, err := os.Create(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics csv: %w", err)
	}

	w := csv.NewWriter(f)

	header := []string{
		"timestamp_unix_ms",
		"frame_index",
		"latency_ms",
		"stall",
		"effective_bitrate_kbps",
	}
	if err = w.Write(header); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to write metrics header: %w", err)
	}
	w.Flush()

	return &MetricsCSVWriter{
		writer: w,
		file:   f,
	}, nil
}

// WriteMetric 写入一条帧级指标，不会在出错时 panic，只打印错误日志。
func (m *MetricsCSVWriter) WriteMetric(metric FrameMetric) {
	if m == nil || m.writer == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record := []string{
		fmt.Sprintf("%d", metric.Timestamp.UnixMilli()),
		fmt.Sprintf("%d", metric.FrameIndex),
		fmt.Sprintf("%.3f", metric.LatencyMillis),
		fmt.Sprintf("%t", metric.Stall),
		fmt.Sprintf("%.3f", metric.EffectiveBitrateKbps),
	}
	if err := m.writer.Write(record); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing metrics CSV: %v\n", err)
		return
	}
	m.writer.Flush()
}

// Close 关闭底层文件句柄。
func (m *MetricsCSVWriter) Close() {
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
			fmt.Fprintf(os.Stderr, "Error closing metrics CSV file: %v\n", err)
		}
	}
}

// filepathDir 是 filepath.Dir 的一个轻量封装，避免在这里直接引入整个 filepath 包，
// 同时保持实现简单。对于常规的 "a/b/c.csv" 路径行为与 filepath.Dir 一致。
func filepathDir(path string) string {
	lastSlash := -1
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash <= 0 {
		return "."
	}
	return path[:lastSlash]
}




