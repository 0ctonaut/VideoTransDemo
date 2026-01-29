# WebRTC 手动运行指南

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

接收到的视频文件（`received.h264`）可以用 FFmpeg 进行质量评估。

### 准备工作

1. **将接收到的 H264 文件封装为 MP4**：
```bash
# 假设帧率为 30fps（根据实际编码帧率调整）
ffmpeg -fflags +genpts -r 30 -i received.h264 -c:v copy received.mp4
```

2. **准备参考视频**：
   - 使用原始视频文件 `Ultra.mp4` 作为参考
   - 或者，如果需要完全相同的编码参数，可以在 server 端同时保存编码后的 H264

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

### 示例脚本

创建一个评估脚本 `evaluate.sh`：

```bash
#!/bin/bash
RECEIVED="$1"
REFERENCE="$2"
FPS="${3:-30}"

# 封装接收到的 H264
echo "Converting received.h264 to MP4..."
ffmpeg -y -fflags +genpts -r $FPS -i "$RECEIVED" -c:v copy received.mp4

# 计算指标
echo "Calculating PSNR..."
ffmpeg -i received.mp4 -i "$REFERENCE" -lavfi "psnr=stats_file=psnr.log" -f null - 2>&1 | grep "average:"

echo "Calculating SSIM..."
ffmpeg -i received.mp4 -i "$REFERENCE" -lavfi "ssim=stats_file=ssim.log" -f null - 2>&1 | grep "SSIM"

echo "Calculating VMAF..."
ffmpeg -i received.mp4 -i "$REFERENCE" \
  -lavfi "libvmaf=log_path=vmaf.json:log_fmt=json" \
  -f null - 2>&1 | grep "VMAF"

echo "Results saved to: psnr.log, ssim.log, vmaf.json"
```

**使用方法**：
```bash
./evaluate.sh session_XXX/received.h264 Ultra.mp4 30
```

脚本会自动：
1. 将 `received.h264` 封装为 MP4
2. 计算 PSNR 并保存到 `*_psnr.log`
3. 计算 SSIM 并保存到 `*_ssim.log`
4. 计算 VMAF 并保存到 `*_vmaf.json`（如果 libvmaf 可用）

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

