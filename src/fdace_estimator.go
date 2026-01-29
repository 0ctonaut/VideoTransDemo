// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//
// fdace_estimator.go - NDTC 中用于估计可用带宽的简化 FDACE 估计器
//
// 说明：
//   - 理论上 FDACE 通过拟合 R/L 与 S/L 的线性关系得到参数 a_n、b_n，
//     再进一步推导出可用带宽 A_n。
//   - 这里实现一个工程上可用的近似版本：
//       * 维护一个滑动窗口，记录最近若干帧的 (S, R, L)；
//       * 通过线性回归估计 a、b（可用于进一步分析）；
//       * 通过简单的 L/R 平均值近似当前可用带宽（bit/s）。

package main

import (
	"math"
	"sync"
)

// FdaceSample 表示一帧的时序与大小信息。
// S: 发送持续时间（秒）；R: 接收持续时间（秒）；L: 帧大小（比特）。
type FdaceSample struct {
	FrameID int
	S       float64
	R       float64
	L       float64
}

// FdaceWindow 维护最近若干帧的样本，并提供线性回归与容量估计。
type FdaceWindow struct {
	mu       sync.Mutex
	samples  []FdaceSample
	maxCount int
}

// NewFdaceWindow 创建一个新的滑动窗口。
func NewFdaceWindow(maxCount int) *FdaceWindow {
	if maxCount <= 0 {
		maxCount = 120 // 默认保留最近约 4 秒（假设 30fps）
	}
	return &FdaceWindow{
		maxCount: maxCount,
		samples:  make([]FdaceSample, 0, maxCount),
	}
}

// UpdateSample 追加一条样本，自动裁剪窗口。
func (w *FdaceWindow) UpdateSample(s FdaceSample) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if s.L <= 0 {
		return
	}
	w.samples = append(w.samples, s)
	if len(w.samples) > w.maxCount {
		// 丢弃最旧的样本
		copy(w.samples[0:], w.samples[len(w.samples)-w.maxCount:])
		w.samples = w.samples[:w.maxCount]
	}
}

// EstimateAR 在当前窗口上拟合 R/L = a * (S/L) + b。
// 返回 (a, b, ok)。若样本过少或数据异常则 ok=false。
func (w *FdaceWindow) EstimateAR() (float64, float64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(w.samples)
	if n < 2 {
		return 0, 0, false
	}

	var sumX, sumY, sumXX, sumXY float64
	valid := 0

	for _, s := range w.samples {
		if s.L <= 0 {
			continue
		}
		x := s.S / s.L // S/L
		y := s.R / s.L // R/L
		if !isFinite(x) || !isFinite(y) {
			continue
		}
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
		valid++
	}

	if valid < 2 {
		return 0, 0, false
	}

	nf := float64(valid)
	den := nf*sumXX - sumX*sumX
	if math.Abs(den) < 1e-12 {
		return 0, 0, false
	}

	a := (nf*sumXY - sumX*sumY) / den
	b := (sumY - a*sumX) / nf

	if !isFinite(a) || !isFinite(b) {
		return 0, 0, false
	}
	return a, b, true
}

// EstimateCapacity 返回一个简化的可用带宽估计（bit/s）。
// 这里直接使用窗口内 L/R 的均值近似 A ≈ E[L/R]。
func (w *FdaceWindow) EstimateCapacity() (float64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var sum, cnt float64
	for _, s := range w.samples {
		if s.R <= 0 {
			continue
		}
		rate := s.L / s.R // bit/s
		if !isFinite(rate) || rate <= 0 {
			continue
		}
		sum += rate
		cnt++
	}
	if cnt < 1 {
		return 0, false
	}
	return sum / cnt, true
}

func isFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}


