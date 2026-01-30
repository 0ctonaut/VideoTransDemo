// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// burst_controller.go - BurstRTC 控制器的简化实现
//
// 说明：
//   - 实现 BurstRTC 风格的 per-frame 预算控制
//   - 维护帧大小统计（均值/方差）和可用带宽估计
//   - 提供 NextFrameBudget 返回目标比特数和 burst fraction

package main

import (
	"math"
	"sync"
	"time"
)

// BurstObservation 表示一帧的发送观测
type BurstObservation struct {
	FrameID   int
	SentBits  int       // 该帧实际发送的总比特数
	SendStart time.Time // 发送开始时间
	SendEnd   time.Time // 发送结束时间
}

// BurstConfig 表示 BurstRTC 控制器的配置参数
type BurstConfig struct {
	FrameInterval time.Duration // 帧周期（例如 1/30s）
	SafetyMargin  float64       // 安全系数（例如 0.7）
	WindowSize    int           // 滑动窗口大小（用于统计）
	BurstFraction float64       // 默认 burst 比例（例如 0.3 表示 30% 的帧数据以 burst 方式发送）
}

// BurstController 保存 BurstRTC 的运行时状态
type BurstController struct {
	mu sync.Mutex

	cfg BurstConfig

	// 滑动窗口：存储最近的帧观测
	observations []BurstObservation
	// 帧大小统计
	frameSizeMean   float64 // 帧大小均值（比特）
	frameSizeVar    float64 // 帧大小方差
	// 可用带宽估计（bit/s）
	availableBps float64
	// 总发送比特数（用于计算平均吞吐）
	totalBits int64
	// 总发送持续时间（用于计算平均吞吐）
	totalDuration time.Duration
}

// NewBurstController 创建一个具有默认参数的 BurstRTC 控制器
func NewBurstController(cfg BurstConfig) *BurstController {
	if cfg.FrameInterval <= 0 {
		cfg.FrameInterval = time.Second / 30 // 默认 30fps
	}
	if cfg.SafetyMargin <= 0 {
		cfg.SafetyMargin = 0.7 // 默认安全系数
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 30 // 默认窗口大小（约 1 秒）
	}
	if cfg.BurstFraction <= 0 {
		cfg.BurstFraction = 0.3 // 默认 30% burst
	}

	return &BurstController{
		cfg:          cfg,
		observations: make([]BurstObservation, 0, cfg.WindowSize),
		availableBps: 0,
	}
}

// UpdateStats 更新控制器状态，基于新的帧观测
func (c *BurstController) UpdateStats(obs BurstObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if obs.SentBits <= 0 {
		return
	}

	// 添加到滑动窗口
	c.observations = append(c.observations, obs)
	if len(c.observations) > c.cfg.WindowSize {
		// 移除最旧的观测
		c.observations = c.observations[1:]
	}

	// 更新总统计
	c.totalBits += int64(obs.SentBits)
	duration := obs.SendEnd.Sub(obs.SendStart)
	if duration > 0 {
		c.totalDuration += duration
	}

	// 更新帧大小统计（均值与方差）
	c.updateFrameSizeStats()

	// 更新可用带宽估计
	if c.totalDuration > 0 {
		c.availableBps = float64(c.totalBits) / c.totalDuration.Seconds()
	} else {
		// fallback：假设 5Mbps
		c.availableBps = 5e6
	}
}

// updateFrameSizeStats 更新帧大小的均值与方差
func (c *BurstController) updateFrameSizeStats() {
	n := len(c.observations)
	if n == 0 {
		return
	}

	var sum float64
	for _, obs := range c.observations {
		sum += float64(obs.SentBits)
	}
	c.frameSizeMean = sum / float64(n)

	// 计算方差
	var varianceSum float64
	for _, obs := range c.observations {
		diff := float64(obs.SentBits) - c.frameSizeMean
		varianceSum += diff * diff
	}
	if n > 1 {
		c.frameSizeVar = varianceSum / float64(n-1)
	} else {
		c.frameSizeVar = 0
	}
}

// NextFrameBudget 返回下一帧的目标比特数和 burst fraction
// 基于当前可用带宽估计和帧大小统计，使用 SafetyMargin 确保不会过度拥塞
func (c *BurstController) NextFrameBudget() (targetBits int, burstFraction float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	A := c.availableBps
	if A <= 0 {
		// fallback：假设 5Mbps
		A = 5e6
	}

	// 目标比特数 = 可用带宽 * 帧间隔 * 安全系数
	// 考虑帧大小方差，可以进一步调整（当前简化版本先不考虑）
	frameIntervalSec := c.cfg.FrameInterval.Seconds()
	targetBitsFloat := A * frameIntervalSec * c.cfg.SafetyMargin
	targetBits = int(targetBitsFloat)
	if targetBits < 1 {
		targetBits = 1
	}

	// 如果帧大小方差较大，可以适当降低 burst fraction（当前版本固定）
	burstFraction = c.cfg.BurstFraction

	// 可选：根据方差调整 burst fraction
	// 如果方差大，说明帧大小波动大，可以降低 burst 比例以减小对队列的冲击
	if c.frameSizeVar > 0 && c.frameSizeMean > 0 {
		coefficientOfVariation := math.Sqrt(c.frameSizeVar) / c.frameSizeMean
		// 如果变异系数大，降低 burst fraction
		if coefficientOfVariation > 0.5 {
			burstFraction *= 0.7
		}
	}

	return targetBits, burstFraction
}

// GetStats 返回当前统计信息（用于调试和日志）
func (c *BurstController) GetStats() (meanBits float64, varianceBits float64, availableBps float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frameSizeMean, c.frameSizeVar, c.availableBps
}



