// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// metrics_summary.go - 汇总统计计算工具
//
// 说明：
//   - 读取 client_metrics.csv，计算整体统计指标
//   - 包括：Average & P99 latency, Stall rate, Effective bitrate

package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// SummaryMetrics 表示汇总统计指标
type SummaryMetrics struct {
	TotalFrames           int     `json:"total_frames"`
	AverageLatencyMs      float64 `json:"average_latency_ms"`
	P99LatencyMs          float64 `json:"p99_latency_ms"`
	StallRate             float64 `json:"stall_rate"`
	EffectiveBitrateKbps  float64 `json:"effective_bitrate_kbps"`
	TotalStallFrames      int     `json:"total_stall_frames"`
	TotalDurationSeconds   float64 `json:"total_duration_seconds"`
}

// CalculateSummaryMetrics 从 client_metrics.csv 计算汇总统计
func CalculateSummaryMetrics(csvPath string) (*SummaryMetrics, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open metrics CSV: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read metrics CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("insufficient data in metrics CSV (need at least header + 1 record)")
	}

	var latencies []float64
	var stallCount int
	var totalBitrateKbps float64
	var bitrateCount int
	var firstTimestamp int64
	var lastTimestamp int64

	// 跳过 header
	for i := 1; i < len(records); i++ {
		record := records[i]
		if len(record) < 5 {
			continue
		}

		// timestamp_ms (相对时间戳), frame_index, latency_ms, stall, effective_bitrate_kbps
		timestampMs, err := strconv.ParseInt(record[0], 10, 64)
		if err != nil {
			continue
		}
		latencyMs, err := strconv.ParseFloat(record[2], 64)
		if err != nil {
			continue
		}
		stall, err := strconv.ParseBool(record[3])
		if err != nil {
			continue
		}
		bitrateKbps, err := strconv.ParseFloat(record[4], 64)
		if err != nil {
			continue
		}

		latencies = append(latencies, latencyMs)
		if stall {
			stallCount++
		}
		if bitrateKbps > 0 {
			totalBitrateKbps += bitrateKbps
			bitrateCount++
		}

		if firstTimestamp == 0 {
			firstTimestamp = timestampMs
		}
		lastTimestamp = timestampMs
	}

	if len(latencies) == 0 {
		return nil, fmt.Errorf("no valid latency data found")
	}

	// 计算平均延迟
	var totalLatency float64
	for _, lat := range latencies {
		totalLatency += lat
	}
	averageLatency := totalLatency / float64(len(latencies))

	// 计算 P99 延迟
	sort.Float64s(latencies)
	p99Index := int(float64(len(latencies)) * 0.99)
	if p99Index >= len(latencies) {
		p99Index = len(latencies) - 1
	}
	p99Latency := latencies[p99Index]

	// 计算 Stall rate
	stallRate := float64(stallCount) / float64(len(latencies))

	// 计算平均有效码率
	avgBitrate := 0.0
	if bitrateCount > 0 {
		avgBitrate = totalBitrateKbps / float64(bitrateCount)
	}

		// 计算总时长（秒）
		// 注意：现在使用相对时间戳，所以 lastTimestamp - firstTimestamp 就是总时长
		totalDuration := float64(lastTimestamp-firstTimestamp) / 1000.0

	return &SummaryMetrics{
		TotalFrames:          len(latencies),
		AverageLatencyMs:    averageLatency,
		P99LatencyMs:         p99Latency,
		StallRate:            stallRate,
		EffectiveBitrateKbps: avgBitrate,
		TotalStallFrames:     stallCount,
		TotalDurationSeconds: totalDuration,
	}, nil
}

// WriteSummaryMetrics 将汇总统计写入 JSON 和文本文件
func WriteSummaryMetrics(summary *SummaryMetrics, sessionDir string) error {
	if sessionDir == "" {
		return fmt.Errorf("sessionDir is empty")
	}

	// 写入 JSON 文件
	jsonPath := filepath.Join(sessionDir, "metrics_summary.json")
	jsonData, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal summary to JSON: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return fmt.Errorf("failed to write JSON summary: %w", err)
	}

	// 写入文本文件（便于阅读）
	txtPath := filepath.Join(sessionDir, "metrics_summary.txt")
	txtContent := fmt.Sprintf(`Frame Metrics Summary
====================
Total Frames:           %d
Average Latency:        %.3f ms
P99 Latency:            %.3f ms
Stall Rate:             %.2f%% (%d frames)
Effective Bitrate:      %.2f kbps
Total Duration:         %.2f seconds
`,
		summary.TotalFrames,
		summary.AverageLatencyMs,
		summary.P99LatencyMs,
		summary.StallRate*100.0,
		summary.TotalStallFrames,
		summary.EffectiveBitrateKbps,
		summary.TotalDurationSeconds,
	)
	if err := os.WriteFile(txtPath, []byte(txtContent), 0o644); err != nil {
		return fmt.Errorf("failed to write text summary: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Summary metrics written to:\n")
	fmt.Fprintf(os.Stderr, "  - %s\n", jsonPath)
	fmt.Fprintf(os.Stderr, "  - %s\n", txtPath)

	return nil
}

