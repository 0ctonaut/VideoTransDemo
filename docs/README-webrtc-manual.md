# WebRTC 手动运行指南

本指南介绍如何使用 `network-ws` 项目进行 WebRTC 视频传输实验，包括基础版本和多种拥塞控制算法（GCC、NDTC、Salsify、BurstRTC）的对比实验。

## 目录

1. [快速开始](#快速开始)
2. [编译方法](#编译方法)
3. [算法概述](#算法概述)
4. [各算法使用方法](#各算法使用方法)
5. [mahimahi 网络模拟](#mahimahi-网络模拟)
6. [视频质量评估](#视频质量评估)
7. [对比实验](#对比实验)
8. [手动运行方式](#手动运行方式)

## 快速开始（推荐方式）

使用启动脚本，自动管理时间戳文件夹和临时文件。

### 终端 1 - Server:
```bash
./server.sh -video Ultra.mp4
# 或指定 IP
./server.sh -video Ultra.mp4 -ip 192.168.100.1
```

脚本会：
- 自动创建时间戳文件夹（格式：`session_2501151430`）
- 将 offer 写入 `session_XXX/offer.txt`
- 等待 answer 文件 `session_XXX/answer.txt`

### 终端 2 - Client:
```bash
# 自动使用最新的 session 文件夹
./client.sh

# 或指定 session 文件夹
./client.sh session_2501151430

# 或指定 IP 和输出文件
./client.sh -ip 192.168.100.2 -output received.h264
```

脚本会：
- 从 session 文件夹读取 `offer.txt`
- 生成 answer 并写入 `answer.txt`
- 接收视频并保存到 `received.h264`（默认在 session 文件夹内）

## 编译方法

### 基础版本

```bash
# 编译基础 client 和 server
make

# 或分别编译
make client
make server
```

### 各算法编译

项目支持四种拥塞控制算法的对比实验：

```bash
# GCC (Google Congestion Control)
make server-gcc
make client-gcc

# NDTC (Network Delivery Time Control)
make server-ndtc
make client-ndtc

# Salsify
make server-salsify
make client-salsify

# BurstRTC (Frame-Bursting Congestion Control)
make server-burst
make client-burst
```

### 一键编译所有算法

```bash
# 编译所有算法（GCC、NDTC、Salsify、BurstRTC）
make all-algorithms
```

编译后的二进制文件位于 `build/` 目录下：
- `build/server-gcc`, `build/client-gcc`
- `build/server-ndtc`, `build/client-ndtc`
- `build/server-salsify`, `build/client-salsify`
- `build/server-burst`, `build/client-burst`

## 算法概述

### GCC (Google Congestion Control)
- **特点**：基于 delay gradient 的拥塞控制，WebRTC 中广泛使用
- **适用场景**：通用 RTC 应用
- **参考文档**：`docs/` 目录下的相关论文

### NDTC (Network Delivery Time Control)
- **特点**：基于 FDACE（Frame Dithering Available Capacity Estimation）的"按时交付"控制
- **优势**：显式控制帧延迟，适合低延迟场景
- **参考文档**：`docs/ndtc-overview.md`

### Salsify
- **特点**：codec-transport 紧耦合的 per-frame 预算控制
- **优势**：纯函数式编码器，支持多候选编码选择
- **参考文档**：`docs/salsify-overview.md`

### BurstRTC (Frame-Bursting Congestion Control)
- **特点**：以帧突发发送为中心的拥塞控制，显式处理 bit-rate variation
- **优势**：通过帧大小统计模型和解析速率控制优化 tail delay
- **参考文档**：`docs/burstrtc-overview.md`

## 各算法使用方法

所有算法都使用统一的脚本接口，支持相同的参数格式。

### GCC

**终端 1 - Server:**
```bash
./scripts/server-gcc.sh --video assets/Ultra.mp4
```

**终端 2 - Client:**
```bash
./scripts/client-gcc.sh --video assets/Ultra.mp4
```

### NDTC

**终端 1 - Server:**
```bash
./scripts/server-ndtc.sh --video assets/Ultra.mp4
```

**终端 2 - Client:**
```bash
./scripts/client-ndtc.sh --video assets/Ultra.mp4
```

### Salsify

**终端 1 - Server:**
```bash
./scripts/server-salsify.sh --video assets/Ultra.mp4
```

**终端 2 - Client:**
```bash
./scripts/client-salsify.sh --video assets/Ultra.mp4
```

### BurstRTC

**终端 1 - Server:**
```bash
./scripts/server-burst.sh --video assets/Ultra.mp4
```

**终端 2 - Client:**
```bash
./scripts/client-burst.sh --video assets/Ultra.mp4
```

### 脚本参数说明

所有 server 脚本支持以下参数：

- `--video <file>`: 视频文件路径（必需）
- `--ip <address>`: 本地 IP 地址（可选，如 192.168.100.1）
- `--session <name>`: 自定义 session 目录名（可选，默认自动生成时间戳）
- `--loop`: 循环播放视频（可选）
- `--mmdelay <ms>`: mahimahi 延迟（毫秒，可选）
- `--mmloss <uplink> <downlink>`: mahimahi 丢包率（两个值，可选）
- `--mmlink <uplink_trace> <downlink_trace>`: mahimahi 链路 trace（可选）

所有 client 脚本支持以下参数：

- `--video <file>`: 参考视频文件路径（用于评估，必需）
- `--session <name>`: 指定 session 目录（可选，默认使用最新的）
- `--ip <address>`: 本地 IP 地址（可选）
- `--max-duration <duration>`: 最大录制时长（如 60s, 5m，可选）
- `--max-size <MB>`: 最大文件大小（MB，可选）

## mahimahi 网络模拟

项目集成了 [mahimahi](http://mahimahi.mit.edu/) 网络模拟器，可以在受控的网络条件下测试不同算法。

### 基本用法

mahimahi 参数通过 server 脚本传递：

```bash
# 只设置延迟（50ms）
./scripts/server-gcc.sh --video assets/Ultra.mp4 --mmdelay 50

# 设置延迟和丢包率（上行 0.01，下行 0.01）
./scripts/server-gcc.sh --video assets/Ultra.mp4 --mmdelay 100 --mmloss 0.01 0.01

# 使用链路 trace 文件
./scripts/server-gcc.sh --video assets/Ultra.mp4 --mmlink uplink.trace downlink.trace

# 组合使用多个参数
./scripts/server-gcc.sh --video assets/Ultra.mp4 \
  --mmdelay 100 \
  --mmloss 0.01 0.01 \
  --mmlink uplink.trace downlink.trace
```

### mahimahi 参数说明

- **`--mmdelay <ms>`**: 单向延迟（毫秒）
  - 示例：`--mmdelay 100` 表示 100ms 延迟

- **`--mmloss <uplink> <downlink>`**: 丢包率（0.0-1.0）
  - 示例：`--mmloss 0.01 0.01` 表示上行和下行各 1% 丢包率
  - **注意**：必须提供两个值（上行和下行）

- **`--mmlink <uplink_trace> <downlink_trace>`**: 链路带宽 trace 文件
  - 示例：`--mmlink uplink.downlink downlink.trace`
  - trace 文件格式参考 mahimahi 文档

### mahimahi 命令嵌套顺序

脚本内部会按以下顺序嵌套 mahimahi 命令：
1. 最外层：`mm-link`（如果指定）
2. 中间层：`mm-delay`（如果指定）
3. 内层：`mm-loss uplink` 和 `mm-loss downlink`（如果指定）

这样可以正确模拟复杂的网络条件。

## 帧级性能指标统计

所有算法实验会自动记录帧级性能指标，包括：

### 指标说明

1. **Average & P99 Frame Latency（平均和 P99 帧延迟）**
   - **端到端延迟**：从 server 发送帧到 client 接收帧的时间差（如果 server metadata 可用）
   - **帧间隔延迟**：相邻帧接收时间差
   - **Average Latency**：所有帧延迟的平均值
   - **P99 Latency**：延迟的 99 百分位数

2. **Stall Rate（卡顿率）**
   - 检测帧间隔 > 2倍正常帧间隔的帧（例如 30fps 时 > 66.7ms）
   - Stall Rate = Stall 帧数 / 总帧数

3. **Effective Bitrate（有效码率）**
   - 基于实际接收的比特数计算
   - 使用滑动窗口（最近 1 秒）计算瞬时码率
   - 汇总统计显示平均有效码率

### 输出文件

每个实验 session 目录下会生成以下文件：

- `frame_metadata.csv`：Server 端记录的每帧发送时间戳
  - 格式：`frame_id, send_start_unix_ms, send_end_unix_ms, frame_bits`
- `client_metrics.csv`：Client 端记录的每帧指标
  - 格式：`timestamp_unix_ms, frame_index, latency_ms, stall, effective_bitrate_kbps`
- `metrics_summary.json`：汇总统计（JSON 格式）
- `metrics_summary.txt`：汇总统计（文本格式，便于阅读）

### 查看汇总统计

汇总统计会在 client 退出时自动计算并显示，也可以在评估脚本完成后查看：

```bash
# 查看 JSON 格式（需要 jq）
jq . session_*/metrics_summary.json

# 查看文本格式
cat session_*/metrics_summary.txt
```

评估脚本 `evaluate.sh` 会自动显示汇总统计（如果存在）。

## 视频质量评估

所有 client 脚本在接收完成后会自动调用 `scripts/evaluate.sh` 进行质量评估。

### 使用的 FFmpeg 版本

本项目默认优先使用你本地自编译、带 `libvmaf` 的 FFmpeg：

- 默认路径：`~/ffmpeg-vmaf/bin/ffmpeg`
- 也可以通过环境变量覆盖：
  - `FFMPEG_BIN`: 指定要使用的 ffmpeg 可执行文件路径
  - `VMAF_MODEL`: 指定 VMAF 模型文件路径（默认：`~/ffmpeg-vmaf/share/model/vmaf_v0.6.1.json`）

示例：

```bash
export FFMPEG_BIN="$HOME/ffmpeg-vmaf/bin/ffmpeg"
export VMAF_MODEL="$HOME/ffmpeg-vmaf/share/model/vmaf_v0.6.1.json"
./scripts/client-gcc.sh --video assets/Ultra.mp4
```

如果找不到上述路径，`evaluate.sh` 会回退到系统中的 `ffmpeg`，此时可能无法计算 VMAF，仅能使用 PSNR/SSIM。

### 自动评估

client 脚本会自动执行以下步骤：

1. 将接收到的 `received.h264` 转换为 MP4（使用 FFmpeg 重新编码修复）
2. 计算 PSNR（峰值信噪比）
3. 计算 SSIM（结构相似性）
4. 按 Netflix 推荐方式计算 VMAF（视频多方法评估融合）：
   - 为参考视频和失真视频设置统一帧率（使用 `-r <FPS>`）
   - 使用 `setpts=PTS-STARTPTS` 对齐 PTS
   - 使用 `libvmaf` 滤镜输出 JSON 日志（包含逐帧和聚合 VMAF 指标）

评估结果保存在 session 目录下：
- `psnr.log`: PSNR 逐帧数据
- `ssim.log`: SSIM 逐帧数据
- `vmaf.json`: VMAF 详细数据（JSON 格式）

### 手动评估

如果需要手动运行评估：

```bash
export FFMPEG_BIN="$HOME/ffmpeg-vmaf/bin/ffmpeg"
./scripts/evaluate.sh <received.h264> <reference.mp4> <fps>
```

示例：
```bash
export FFMPEG_BIN="$HOME/ffmpeg-vmaf/bin/ffmpeg"
./scripts/evaluate.sh session_gcc_2601291323/received.h264 assets/Ultra.mp4 30
```

## 对比实验

### 实验流程

1. **准备测试视频**：
   ```bash
   # 确保有测试视频文件
   ls assets/Ultra.mp4
   ```

2. **运行不同算法的实验**（在同一网络条件下）：
   ```bash
   # 终端 1: GCC
   ./scripts/server-gcc.sh --video assets/Ultra.mp4 --mmdelay 100 --mmloss 0.01 0.01
   # 终端 2: GCC client
   ./scripts/client-gcc.sh --video assets/Ultra.mp4
   
   # 终端 1: NDTC
   ./scripts/server-ndtc.sh --video assets/Ultra.mp4 --mmdelay 100 --mmloss 0.01 0.01
   # 终端 2: NDTC client
   ./scripts/client-ndtc.sh --video assets/Ultra.mp4
   
   # 重复其他算法...
   ```

3. **对比结果**：
   - 查看各 session 目录下的 metrics CSV 文件
   - 对比 PSNR/SSIM/VMAF 评估结果
   - 分析帧延迟、丢包率、有效码率等指标

### Session 目录结构

每个实验会创建一个 session 目录（格式：`session_{algorithm}_{timestamp}`），包含：

```
session_gcc_2601291323/
├── offer.txt              # WebRTC offer
├── answer.txt             # WebRTC answer
├── received.h264          # 接收到的原始 H.264 流
├── repaired.mp4           # 修复后的 MP4（由 evaluate.sh 生成）
├── psnr.log              # PSNR 评估结果
├── ssim.log              # SSIM 评估结果
├── vmaf.json             # VMAF 评估结果（如果支持）
└── {algorithm}_server_metrics.csv  # Server 端统计（如果实现）
```

### 对比指标

可以对比以下指标：

- **视频质量**：PSNR、SSIM、VMAF（平均值和分布）
- **帧延迟**：平均延迟、P95/P99 延迟
- **码率利用率**：有效码率、峰值码率
- **丢包率**：网络丢包、重传率
- **卡顿率**：Stall rate

## 手动运行方式（不使用脚本）

## 分两个终端运行 Server 和 Client

### 方式 A：使用临时文件（推荐，自动化）

使用临时文件可以避免手动复制粘贴，server 会自动读取。

**终端 1 - Server:**
```bash
# 创建临时文件
ANSWER_FILE=$(mktemp)
echo "Answer file: $ANSWER_FILE"

# 启动 server，指定 answer 文件
./server -video Ultra.mp4 -answer-file "$ANSWER_FILE"
```

Server 会输出 offer，然后自动等待 answer 文件。

**终端 2 - Client:**
```bash
# 使用相同的 answer 文件路径（从终端1复制）
ANSWER_FILE="/tmp/tmp.xxxxx"  # 替换为实际的路径

# 将 server 输出的 offer 复制粘贴到这里
echo "OFFER_BASE64" | ./client -answer-file "$ANSWER_FILE"
```

Client 会自动将 answer 写入文件，server 检测到文件后会自动读取并建立连接。

### 方式 B：手动复制粘贴（传统方式）

### 方法 1：使用 localhost（同一台机器）

**终端 1 - Server:**
```bash
./server -video Ultra.mp4
```

Server 会输出 base64 编码的 offer，例如：
```
eyJ0eXBlIjoib2ZmZXIiLCJzZHAiOiJ2PTBcclxu...
```

**终端 2 - Client:**
```bash
# 将上面输出的 offer 复制粘贴到这里
echo "eyJ0eXBlIjoib2ZmZXIiLCJzZHAiOiJ2PTBcclxu..." | ./client
```

Client 会输出 base64 编码的 answer，例如：
```
eyJ0eXBlIjoiYW5zd2VyIiwic2RwIjoi...
```

**回到终端 1 - Server:**
将 client 输出的 answer 复制粘贴到终端 1，然后按 Enter。

### 方法 2：使用局域网 IP

**终端 1 - Server:**
```bash
./server -video Ultra.mp4 -ip 192.168.100.1
```

**终端 2 - Client:**
```bash
# 将 server 输出的 offer 复制粘贴到这里
echo "offer_base64_string" | ./client -ip 192.168.100.2
```

**回到终端 1 - Server:**
将 client 输出的 answer 复制粘贴到终端 1，然后按 Enter。

## 参数说明

### Server 参数
- `-video <file>`: 视频文件路径（必需）
- `-ip <address>`: 本地 IP 地址（可选，如 192.168.100.1）
- `-answer-file <file>`: Answer 文件路径（可选，如果指定，从文件读取 answer；否则从 stdin 读取）

### Client 参数
- `-output <file>`: 输出文件路径（默认：received.h264）
- `-ip <address>`: 本地 IP 地址（可选，如 192.168.100.2）
- `-answer-file <file>`: Answer 文件路径（可选，如果指定，将 answer 写入文件；否则输出到 stdout）

## 视频质量评估（PSNR / SSIM / VMAF）

> **注意**：使用脚本时，评估会自动执行。本节介绍手动评估方法。

接收到的视频文件（`received.h264`）可以用 FFmpeg 进行质量评估。

### 准备工作

1. **将接收到的 H264 文件封装为 MP4**：
```bash
# 假设帧率为 30fps（根据实际编码帧率调整）
# 注意：使用重新编码而不是 copy，以修复可能的流错误
ffmpeg -fflags +genpts -err_detect ignore_err -r 30 -i received.h264 \
  -c:v libx264 -preset veryfast -crf 18 repaired.mp4
```

2. **准备参考视频**：
   - 使用原始视频文件 `Ultra.mp4` 作为参考
   - 确保参考视频与接收视频的分辨率、帧率一致

### 计算 PSNR（峰值信噪比）

```bash
ffmpeg -i received.mp4 -i Ultra.mp4 \
  -lavfi "psnr=stats_file=psnr.log" \
  -f null -
```

- 终端会显示平均 PSNR 值
- `psnr.log` 文件包含逐帧的 PSNR 数据

### 计算 SSIM（结构相似性）

```bash
ffmpeg -i received.mp4 -i Ultra.mp4 \
  -lavfi "ssim=stats_file=ssim.log" \
  -f null -
```

- 终端会显示平均 SSIM 值（范围 0-1，越接近 1 越好）
- `ssim.log` 文件包含逐帧的 SSIM 数据

### 计算 VMAF（视频多方法评估融合）

**前提**：需要 FFmpeg 编译时包含 libvmaf 支持

```bash
# 使用默认 VMAF 模型
ffmpeg -i received.mp4 -i Ultra.mp4 \
  -lavfi "libvmaf=log_path=vmaf.json:log_fmt=json" \
  -f null -
```

或者指定模型路径：
```bash
ffmpeg -i received.mp4 -i Ultra.mp4 \
  -lavfi "libvmaf=model_path=/usr/share/model/vmaf_v0.6.1.json:log_path=vmaf.json:log_fmt=json" \
  -f null -
```

- 终端会显示平均 VMAF 分数（范围 0-100，越高越好）
- `vmaf.json` 文件包含详细的逐帧指标（JSON 格式）

### 同时计算多个指标

```bash
ffmpeg -i received.mp4 -i Ultra.mp4 \
  -lavfi "[0:v][1:v]psnr=stats_file=psnr.log;[0:v][1:v]ssim=stats_file=ssim.log;[0:v][1:v]libvmaf=log_path=vmaf.json:log_fmt=json" \
  -f null -
```

### 注意事项

1. **视频对齐**：确保两个视频的时长、分辨率、帧率一致
2. **时间同步**：如果视频有延迟或丢帧，可能需要先对齐时间戳
3. **VMAF 模型**：不同版本的 VMAF 模型可能给出不同的分数，建议使用相同版本进行比较
4. **性能**：VMAF 计算较慢，建议先用 PSNR/SSIM 快速评估

### 使用项目自带的评估脚本

项目已提供 `scripts/evaluate.sh` 脚本，使用方法：

```bash
./scripts/evaluate.sh <received.h264> <reference.mp4> <fps>
```

示例：
```bash
./scripts/evaluate.sh session_gcc_2601291323/received.h264 assets/Ultra.mp4 30
```

脚本会自动：
1. 将 `received.h264` 转换为 MP4（使用重新编码修复）
2. 计算 PSNR 并保存到 `psnr.log`
3. 计算 SSIM 并保存到 `ssim.log`
4. 计算 VMAF 并保存到 `vmaf.json`（如果 libvmaf 可用）

**注意**：client 脚本会自动调用此评估脚本，通常不需要手动运行。

## 注意事项

1. **IP 地址选择**：
   - 如果使用 localhost，两端都不需要指定 `-ip`
   - 如果使用局域网 IP，确保两端在同一网段或可达
   - Server 和 Client 的 IP 应该不同（除非使用 localhost）

2. **SDP 交换**：
   - Offer 和 Answer 都是 base64 编码的 JSON
   - 复制时要包含完整的字符串（通常很长，几千个字符）
   - 确保没有额外的空格或换行

3. **连接建立**：
   - 连接建立后，server 会开始发送视频
   - client 会接收视频并保存到文件
   - 按 Ctrl+C 停止

## 示例完整流程

**终端 1:**
```bash
$ ./server -video Ultra.mp4 -ip 192.168.100.1
Starting ICE gathering (LAN mode, IP: 192.168.100.1, fixed port range 50000-50100)...
Waiting for ICE gathering to complete...
ICE gathering completed
Offer written to stdout (9404 bytes)
eyJ0eXBlIjoib2ZmZXIiLCJzZHAiOiJ2PTBcclxu...  # 复制这整行
Waiting for answer from client...
Paste the answer here and press Enter: 
# 在这里粘贴 answer，然后按 Enter（必须按 Enter！）
```

**终端 2:**
```bash
$ echo "eyJ0eXBlIjoib2ZmZXIiLCJzZHAiOiJ2PTBcclxu..." | ./client -ip 192.168.100.2
# 输出 answer
eyJ0eXBlIjoiYW5zd2VyIiwic2RwIjoi...  # 复制这整行，粘贴到终端 1
```

