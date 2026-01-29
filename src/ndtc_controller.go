// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// ndtc_controller.go - NDTC 控制器的简化实现
//
// 说明：
//   - 负责将 FDACE 的容量估计 A_n 转换为每帧的目标大小 F_n 和发送持续时间（pacing）。
//   - 采用简化版 AIMD 逻辑：在无丢包时缓慢增加容量估计，在出现丢包时乘性减小。

package main

import (
	"math/rand"
	"sync"
	"time"
)

// NdtcConfig 表示 NDTC 控制器的配置参数。
type NdtcConfig struct {
	TFrame time.Duration // 帧周期 T_F
	TSend  time.Duration // 目标发送持续时间 T_S
	TRecv  time.Duration // 目标接收持续时间 T_R

	// AIMD 参数
	AiStep  float64 // 加性增加比例（例如 0.05 表示每个稳定周期增加 5%）
	MdRatio float64 // 乘性减小比例（例如 0.5 表示丢包时减半）
}

// NdtcController 保存 NDTC 的运行时状态。
type NdtcController struct {
	mu sync.Mutex

	cfg NdtcConfig

	// 当前容量估计（bit/s）
	capacityBps float64
	// 最近一次外部容量估计（供调试）
	lastEstimatedBps float64
}

// NewNdtcController 创建一个具有默认参数的控制器。
func NewNdtcController() *NdtcController {
	// 默认按 30fps 配置
	frame := time.Second / 30
	return &NdtcController{
		cfg: NdtcConfig{
			TFrame: frame,
			// 发送时间 < 接收时间 < 帧周期
			TSend:  frame * 7 / 10,
			TRecv:  frame * 8 / 10,
			AiStep: 0.05,
			MdRatio: 0.5,
		},
		capacityBps: 0,
	}
}

// SetConfig 用于覆盖默认配置。
func (c *NdtcController) SetConfig(cfg NdtcConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
}

// OnCapacityEstimate 接收来自 FDACE 的容量估计 A（bit/s），并进行平滑。
func (c *NdtcController) OnCapacityEstimate(A float64) {
	if A <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastEstimatedBps = A
	if c.capacityBps <= 0 {
		c.capacityBps = A
		return
	}

	// 简单的指数平滑，避免剧烈抖动
	const alpha = 0.1
	c.capacityBps = alpha*A + (1-alpha)*c.capacityBps
}

// OnLossEvent 在检测到丢包时调用，做乘性减小。
func (c *NdtcController) OnLossEvent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacityBps <= 0 {
		return
	}
	if c.cfg.MdRatio <= 0 || c.cfg.MdRatio >= 1 {
		c.cfg.MdRatio = 0.5
	}
	c.capacityBps *= c.cfg.MdRatio
}

// OnNoLossPeriod 在稳定无丢包一段时间后调用，做加性增加。
func (c *NdtcController) OnNoLossPeriod() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacityBps <= 0 {
		return
	}
	if c.cfg.AiStep <= 0 {
		c.cfg.AiStep = 0.05
	}
	c.capacityBps *= (1 + c.cfg.AiStep)
}

// NextFrameBudget 返回下一帧的目标大小（比特）和发送持续时间（包含轻微抖动）。
// 若当前容量估计不足，则使用一个保守的缺省值。
func (c *NdtcController) NextFrameBudget() (frameBits int, pacingDuration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	A := c.capacityBps
	if A <= 0 {
		// fallback：假设 5Mbps
		A = 5e6
	}

	// F_n = T_R * A_n
	Trecv := c.cfg.TRecv
	if Trecv <= 0 {
		Trecv = time.Second / 30 * 8 / 10
	}
	frameBits = int(Trecv.Seconds() * A)
	if frameBits < 1 {
		frameBits = 1
	}

	// pacing 以 T_S 为中心做 ±10% 抖动
	Tsend := c.cfg.TSend
	if Tsend <= 0 {
		Tsend = time.Second / 30 * 7 / 10
	}
	jitterFactor := 0.1
	j := 1 + jitterFactor*(rand.Float64()*2-1) // [1-0.1, 1+0.1]
	pacingDuration = time.Duration(float64(Tsend) * j)

	return frameBits, pacingDuration
}


