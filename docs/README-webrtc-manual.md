# 操作指南

## 一、快速开始：使用脚本（推荐）

建议始终使用 `scripts/` 目录下的脚本，它们会自动：
- 创建时间戳 session 目录（如：`session_2601271436`）
- 管理 `offer.txt` / `answer.txt`
- 将接收的视频写入 `session_xxxx/received.h264`

### 1. Server 端（终端 1）

cd ~/network-ws

# 最简单用法：仅发送本地视频
./scripts/server.sh -video assets/Ultra.mp4

# 指定 Server 使用的 IP（如虚拟网卡环境）
./scripts/server.sh -video assets/Ultra.mp4 -ip 192.168.100.1

# 加入简单的网络仿真（示例：50ms 时延 + 0.1% 丢包）
./scripts/server.sh -video assets/Ultra.mp4 --mmdelay 50 --mmloss 0.001脚本会打印类似信息：
- `Session directory: session_2601271436`
- `Offer file: session_2601271436/offer.txt`
- `Answer file: session_2601271436/answer.txt`

### 2. Client 端（终端 2）

cd ~/network-ws

# 自动选择最新的 session 目录
./scripts/client.sh

# 或手动指定 session 目录
./scripts/client.sh -session session_2601271436

# 如果 server 在指定 IP（例如虚拟网卡对端）
./scripts/client.sh -ip 192.168.100.2

Client 会在对应的 session 目录下写入：
- `received.h264`：从 WebRTC 流中提取出的 H.264 码流

> 提示：默认会有简单的进度输出（包数、写入字节数、耗时等），超过设定的最大时长/文件大小会自动退出。

---

## 二、脚本参数说明

### 1. `scripts/server.sh` 参数

- **必选**
  - `-video <file>`：要发送的视频文件路径，例如 `assets/Ultra.mp4`

- **常用可选参数**
  - `-ip <address>`：Server 绑定的本地 IP（如 `192.168.100.1`）。不指定时自动检测。
  - `-loop`：循环播放视频，发完后从头再来。
  - `-netns`：在 `setup.zsh` 创建的 `server` 网络命名空间中运行。

- **Mahimahi 网络仿真相关**
  - `--mmdelay <ms>`：单向时延（毫秒），如 `--mmdelay 50`。
  - `--mmloss <p>`：丢包率（0~1 之间），如 `--mmloss 0.001` 表示 0.1% 丢包。
  - `--mmlink <up> [down]`：手动指定 trace 文件：
    - `--mmlink /usr/local/share/mahimahi/traces/TMobile-LTE-driving.up`
      - 上下行都使用同一条 trace。
    - `--mmlink up.trace down.trace`
      - 分别为上行、下行指定不同的 trace。
  - `--mmlink-default`：不手动指定时，使用 Mahimahi 默认的 `TMobile-LTE-driving.{up,down}`。

> 注意：Mahimahi 的带宽/时延/丢包是在 `mm-link` / `mm-delay` / `mm-loss` 这一层实现的，对 WebRTC / Pion 来说就是在一个“受限网络”上发送 RTP。

### 2. `scripts/client.sh` 参数

- **常用**
  - `-ip <server_ip>`：Server 的 IP 地址（如 `192.168.100.1` / `192.168.100.2`）。不指定时会自动检测。
  - `-session <dir>`：显式指定要使用的 session 目录（默认自动选择最新的 `session_*`）。
  - `-netns`：在 `setup.zsh` 创建的 `client` 网络命名空间中运行。

- **安全退出控制**
  - `-max-duration <sec>`：接收最长时长（秒），如 `-max-duration 60`。
  - `-max-size <bytes>`：接收文件最大体积（字节），超过会停止写入并退出。

- **Mahimahi（用法与 server 一致）**
  - `--mmdelay <ms>`
  - `--mmloss <p>`
  - `--mmlink <up> [down]`
  - `--mmlink-default`

> 一般情况下，为避免双端都在不同 Mahimahi namespace 里互相看不到，**建议主要在一侧（通常是 server）开 Mahimahi**；或者两边都加 `-netns`，先放到同一个虚拟网络命名空间，再在其中跑 Mahimahi。

---

## 三、收到 H.264 后的“修复 + 封装”流程

网络有丢包时，`session_xxxx/received.h264` 里可能会包含不完整的 NALU，直接 `-c:v copy` 封装成 MP4 很容易导致：
- 生成的视频无法播放；
- 或只有极少几帧。

推荐先让 FFmpeg 进行一次“解码 + 重编码”，尽量把可解部分抢救出来。

假设接收目录为 `session_2601271436`：

cd ~/network-ws

ffmpeg -fflags +genpts -err_detect ignore_err \
       -r 30 -i session_2601271436/received.h264 \
       -c:v libx264 -preset veryfast -crf 18 \
       repaired.mp4说明：
- **`-fflags +genpts`**：生成时间戳，避免缺失导致的时间线问题。
- **`-err_detect ignore_err`**：忽略部分解码错误，能解多少解多少。
- **`-c:v libx264 -preset veryfast -crf 18`**：用 x264 重编码，速度-质量折中。
- 输出的 `repaired.mp4`：
  - 在有丢包时可能会花屏、跳帧、卡顿；
  - 但通常可以**正常播放**，也可以作为质量评测的“退化视频”。

如果网络条件很好（基本无丢包），也可以用简单封装版本：

ffmpeg -fflags +genpts -r 30 -i session_2601271436/received.h264 -c:v copy received.mp4---

## 四、质量评估（PSNR / SSIM / VMAF）

这里假设：
- **参考视频**：`assets/Ultra.mp4`（或你自己的源视频）
- **退化视频**：上一步生成的 `repaired.mp4`

### 1. 计算 PSNR（峰值信噪比）

ffmpeg -i repaired.mp4 -i assets/Ultra.mp4 \
       -lavfi "psnr=stats_file=psnr.log" \
       -f null -- 终端会显示平均 PSNR 值；
- `psnr.log` 包含逐帧 PSNR。

### 2. 计算 SSIM（结构相似性）

ffmpeg -i repaired.mp4 -i assets/Ultra.mp4 \
       -lavfi "ssim=stats_file=ssim.log" \
       -f null -- 终端会显示平均 SSIM（0~1，越接近 1 越好）；
- `ssim.log` 包含逐帧 SSIM。

### 3. 计算 VMAF（需要 FFmpeg 编译时启用 libvmaf）

ffmpeg -i repaired.mp4 -i assets/Ultra.mp4 \
       -lavfi "libvmaf=log_path=vmaf.json:log_fmt=json" \
       -f null -或指定模型路径：

ffmpeg -i repaired.mp4 -i assets/Ultra.mp4 \
       -lavfi "libvmaf=model_path=/usr/local/share/model/vmaf_v0.6.1.json:log_path=vmaf.json:log_fmt=json" \
       -f null -- 终端会显示平均 VMAF 分数（0~100，越高越好）；
- `vmaf.json` 里有逐帧详细指标。

### 4. 使用现成脚本 `scripts/evaluate.sh`

项目中已经提供了一个辅助脚本，可以简化评估流程：

cd ~/network-ws

# 假设 session 目录为 session_2601271436
./scripts/evaluate.sh session_2601271436脚本会自动：
- 使用合适的 FFmpeg 参数处理 `received.h264`（包括时间戳、错误忽略等）；
- 生成中间视频文件；
- 计算 PSNR / SSIM / VMAF，并把结果输出/写入日志文件。

> 建议：做实验时记录好每次运行的参数（`--mmdelay` / `--mmloss` / `--mmlink` 等）以及对应的 VMAF/PSNR/SSIM，方便后续画曲线和对比。

---

## 五、常见注意事项

- **帧率与分辨率对齐**
  - 评测前应确保参考视频与退化视频分辨率一致；
  - 帧率不一致时，尽量通过 `-r` 或前处理对齐。

- **丢包过大导致“无法解码”**
  - 如果即使用 `-err_detect ignore_err` 和重编码仍几乎解不出帧，可以把这组条件标记为“decode failure”，而不是硬算 VMAF/PSNR。
  - 做实验时建议从较小丢包率开始（如 `--mmloss 0.0005`、`0.001`），保证仍能解出一段有代表性的退化视频。

- **Mahimahi 与 netns 混用**
  - 同时开启 `-netns` 和 Mahimahi 时，要确保 server/client 处在 **可互通** 的网络环境中；
  - 通常建议先用 `setup.zsh` 建好 `server/client` 命名空间，然后在对应命名空间里跑 `server.sh` / `client.sh`，必要时只在一侧加 Mahimahi。 