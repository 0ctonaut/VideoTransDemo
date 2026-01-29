// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
//go:build !js && salsify
// +build !js,salsify
//
// salsify_controller.go - Salsify 风格的按帧 bit 预算控制器（工程近似版）

package main

import (
	"sync"
	"time"
)

// SalsifyObservation 表示一帧的发送观测数据（仅发送侧，客户端反馈暂未接入）。
type SalsifyObservation struct {
	FrameID      int
	SentBits     int
	SendStart    time.Time
	SendEnd      time.Time
	LossDetected bool
}

// SalsifyConfig 控制器配置。
type SalsifyConfig struct {
	FrameInterval time.Duration // 期望帧间隔，例如 1/30s

	LatencyTarget time.Duration // 目标排队+传输延迟上限（目前仅用于未来扩展）

	// SafetyMargin 用于在估计吞吐上打折，类似 Sprout 中选择较保守 quantile。
	SafetyMargin float64

	// WindowSize 用于计算滑动窗口平均吞吐。
	WindowSize int
}

// SalsifyController 是一个简化版的 Salsify per-frame 预算控制器。
// 目前只在发送侧基于历史发送速率估计下一帧预算。
type SalsifyController struct {
	mu sync.Mutex

	cfg SalsifyConfig

	observations []SalsifyObservation

	// 派生统计
	avgThroughputBitsPerSec float64
	lossRate                float64
}

// NewSalsifyController 创建一个新的控制器实例。
func NewSalsifyController(cfg SalsifyConfig) *SalsifyController {
	if cfg.FrameInterval <= 0 {
		cfg.FrameInterval = time.Second / 30
	}
	if cfg.SafetyMargin <= 0 || cfg.SafetyMargin > 1 {
		cfg.SafetyMargin = 0.7
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 30
	}
	if cfg.LatencyTarget <= 0 {
		cfg.LatencyTarget = 200 * time.Millisecond
	}

	return &SalsifyController{
		cfg:          cfg,
		observations: make([]SalsifyObservation, 0, cfg.WindowSize),
	}
}

// UpdateStats 记录一帧的发送观测，并更新滑动窗口统计。
func (c *SalsifyController) UpdateStats(obs SalsifyObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.observations = append(c.observations, obs)
	if len(c.observations) > c.cfg.WindowSize {
		// 仅保留最近 WindowSize 帧
		c.observations = c.observations[len(c.observations)-c.cfg.WindowSize:]
	}

	var totalBits int64
	var totalDurationSec float64
	var lossCount int

	for _, o := range c.observations {
		totalBits += int64(o.SentBits)
		d := o.SendEnd.Sub(o.SendStart).Seconds()
		if d <= 0 {
			// 如果单帧发送时间过短，则按 FrameInterval 近似
			d = c.cfg.FrameInterval.Seconds()
		}
		totalDurationSec += d
		if o.LossDetected {
			lossCount++
		}
	}

	if totalDurationSec > 0 {
		c.avgThroughputBitsPerSec = float64(totalBits) / totalDurationSec
	}

	if len(c.observations) > 0 {
		c.lossRate = float64(lossCount) / float64(len(c.observations))
	}
}

// NextFrameBudget 估计下一帧可用的 bit 预算（工程近似版）。
// 思路：
//   - 以滑动窗口平均吞吐 * 帧间隔 * SafetyMargin 作为预算；
//   - 当 lossRate 较高时进一步降低预算。
func (c *SalsifyController) NextFrameBudget() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果还没有观测，就采用一个保守的初始预算，例如 500kbps * 1/30s。
	throughput := c.avgThroughputBitsPerSec
	if throughput <= 0 {
		throughput = 500_000 // 500 kbps
	}

	budget := throughput * c.cfg.FrameInterval.Seconds() * c.cfg.SafetyMargin

	// 简单根据丢包率做回退：超过 2% 时每 1% 再降低 10%。
	if c.lossRate > 0.02 {
		over := c.lossRate - 0.02
		scale := 1.0 - over*10.0
		if scale < 0.3 {
			scale = 0.3
		}
		budget *= scale
	}

	if budget < 10_000 {
		budget = 10_000
	}
	if budget > 5_000_000 {
		budget = 5_000_000
	}

	return int(budget)
}


