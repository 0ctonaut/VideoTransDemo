### BurstRTC（Frame-Bursting Congestion Control）算法综述

> 基于论文 *“Tackling Bit-Rate Variation of RTC through Frame-Bursting Congestion Control”*（Jia et al., 2024，`Jia 等 - 2024 - Tackling Bit-Rate Variation of RTC Through Frame-Bursting Congestion Control.pdf`）的总结与工程映射。
>
> 本文档结构与 `ndtc-overview.md`、`salsify-overview.md` 保持一致，便于后续与 GCC / NDTC / Salsify 等算法对比。

---

## 1. 背景与问题定义

实时通讯（RTC）应用（如视频会议、云游戏、XR/VR）在近几年快速发展，但大量测量工作表明，其 QoE 依然存在显著问题：

- **交互延迟高**：超过 25% 的时间里用户感知到明显延迟，约 5% 的时间应用因时延过大几乎不可用；
- **视频冻结 / rebuffering 频繁**：用户平均有约 3.6% 的时间处于“卡住”状态；
- **画质波动大**：在蜂窝/无线网络中，码率与质量经常出现剧烈变化。

Jia 等指出，一个关键、但长期被忽视的原因是：

- 现有 RTC 拥塞控制（如 GCC、BBR 变体等）延续了传统 TCP 的假设：**发送端始终有连续的字节流等待发送**（“always-backlogged sender”）；  
- 然而在 RTC 中，视频是**逐帧编码**的，编码器输出的比特率随时间有剧烈波动（bit-rate variation），发送端缓冲并不总是“填满”。

这种错配导致两个典型问题（论文 Figure 1）：

- **Overshooting（过冲）**：  
  - 当编码器输出实际码率 **高于** CC 允许的发送速率时，大量包在发送缓冲累积，超过 150ms 的数据排队；  
  - 帧必须“等前面包先发完”才能上路 → 帧端到端延迟显著升高。
- **Undershooting（欠冲）**：  
  - 当实际编码码率 **低于** CC 发送速率时，发送缓冲经常为空；  
  - 拥塞控制缺乏足够的样本包来推断可用带宽，无法正确利用链路能力。

**视频编码器固有的码率波动**（bit-rate variation）是这一切的根源：  
即使设定了目标平均码率，由于内容时空复杂度的变化、GOP 结构和关键帧，实际每帧输出大小会围绕目标码率大幅波动。

BurstRTC 的核心观点是：

- 传统“网络导向的 CC”没有把 **按帧的突发发送** 与 **编码器码率波动** 当成一等公民；
- 需要一个专门面向 RTC 的 **帧突发拥塞控制（Frame-Bursting CC）**，在帧粒度上联合建模带宽、队列和帧大小分布，从而同时控制帧延迟与码率利用率。

---

## 2. 相关工作与现状

### 2.1 传统 TCP / BBR 等面向“连续流”的 CC

- 经典 TCP 变种（NewReno、CUBIC、Vegas 等）和 BBR 设计的主要假设是：  
  **发送端总是有足够多的数据可以立刻发送**。
- 拥塞控制的目标在于：  
  - 通过调整 congestion window / pacing rate 使得吞吐接近瓶颈链路容量；  
  - 依赖 RTT、丢包、排队时延等信号来调节发送速率；  
  - 默认应用在长数据流（文件传输、HTTP、视频点播）场景。

在 RTC 中，如果直接套用这些机制：

- 当编码器输出略低于估计速率时，链路被“饿着”，可用带宽利用不足；  
- 当编码器输出突然突刺时，CC 还没来得及降速就已经造成队列膨胀和 tail 延迟增大。

### 2.2 RTC 领域的 GCC / SCReAM / SQP / Pudica 等

- **GCC（Google Congestion Control）**［13］：  
  - 在 WebRTC 中广泛部署，使用基于 packet-train 的 delay gradient 来推断拥塞程度；  
  - 仍然假设有足够数据可发，目标倾向于“吃满带宽”。
- **SCReAM**［24］：  
  - 控制在途字节数（bytes-in-flight），以单向时延和丢包为反馈调节窗口；  
  - 对 RTC 更友好，但仍不是按帧建模。
- **SQP**［22］、**Pudica**［23］等：  
  - 更强调低尾延迟，对队列长度和延迟分布做专门优化；  
  - 但多数情况下仍把流量视为“连续流 + occasional bursts”，没有深入建模编码器的帧级波动。

总体来看，这些方案在多 trace、长时间平均指标上表现不错，但：

- **没有显式、系统性处理“逐帧突发 + 帧大小分布 + 编码波动”三者的组合效应**；
- 当场景更偏向交互式低延迟（云游戏、实时协作）时，队列膨胀和 tail 延迟问题依旧严重。

### 2.3 Salsify / NDTC / BurstRTC：帧级控制与架构创新

与上述“网络导向 + 连续流”思路不同：

- **Salsify**［19］：  
  - 通过纯函数式 VP8 + Sprout 风格模型，在每一帧级别做 bit budget 和候选编码选择；  
  - 强调 codec–transport 的 tight integration。
- **NDTC**（Ageneau & Armitage 2025，见 `ndtc-overview.md`）：  
  - 利用 FDACE 通过帧发送/接收持续时间估计可用容量 A_n；  
  - 目标是“按时交付帧”，而非吃满带宽。
- **BurstRTC（本文）**：  
  - 指出 **bit-rate variation** 是 RTC 的根本特征；  
  - 设计了一个以 **frame bursting** 为中心的 CC 框架：  
    - 每帧以“帧突发 + pacing”发送；  
    - 网络反馈直接控制 **视频 target bit-rate**；  
    - 使用显式的帧大小分布（高斯模型）与帧延迟模型进行解析速率控制。

可以把 BurstRTC 看作是：  
**在 Salsify/NDTC 强调 per-frame 控制的基础上，更系统化地引入“帧大小统计模型 + 帧延迟解析建模 + 帧突发采样”的一套 CC 设计。**

---

## 3. BurstRTC 总体设计与创新点

BurstRTC 的总体目标：

- 针对 RTC 中“编码器码率波动 + 帧级离散性”的特性，  
- 通过 **帧突发发送（frame bursting）** 与 **解析速率控制（analytic rate control）**，  
- 同时提升可用带宽利用率和降低帧时延（尤其是 tail delay）。

核心设计块（design blocks）包括：

1. **Bandwidth Sampler（带宽采样器）**  
   - 通过在帧发送中交替使用 **突发（burst）** 与 **pacing**，估计路径容量和背景流量占用；
   - 兼顾容量探测（类似 packet trains）与公平性。
2. **Frame Size Model（帧大小模型）**  
   - 将编码后帧大小视为高斯随机变量 \\(S \\sim \\mathcal{N}(\\mu_S, \\sigma_S^2)\\)；  
   - 明确考虑帧大小的方差与 tail，避免只依赖平均值导致排队风险。
3. **Frame Delay Model（帧延迟模型）**  
   - 在给定带宽、背景流量和帧大小分布的条件下，解析地预测 future frame 的排队/传输延迟；  
   - 特别关注 tail frame delay。
4. **Analytic Rate Control（解析速率控制）**  
   - 结合上述三个模型，在满足时延约束的前提下，解析或近似求解**最优目标视频码率/帧大小**；  
   - 避免传统 AIMD / gradient-based CC 那种 trial-and-error 式的慢收敛。

用一句话概括：  
**BurstRTC 把“每一帧的 bit 数和发送节奏”当作一等控制变量，用统计模型和解析方法在“带宽利用率 vs 延迟”之间做精细权衡。**

---

## 4. 算法理论细节（贴近原文）

### 4.1 带宽采样：突发 + pacing 的结合

#### 4.1.1 纯 packet train 的局限

经典的 packet train 技术［21,35］通过发出一串紧密的包列车，利用到达间隔推断链路容量。但在 RTC 场景中：

- 纯粹的突发探测**忽略背景流量**：它测到的是接近“裸路径容量”，而非“当前可用带宽”；
- 突发本身会**扰动队列**，给其它流量带来较大瞬时冲击；
- 与编码器的帧结构并不天然对齐（可能在 GOP 边界、关键帧处干扰更大）。

#### 4.1.2 BurstRTC 的 Bandwidth Sampler

BurstRTC 的带宽采样器采用一个更细粒度的策略：

- 对每一帧：  
  - 把帧的部分比特作为 **burst** 子区间，在较短时间窗口内集中发送，以形成“微型 packet train”；  
  - 剩余比特以 **pacing** 的方式平滑发出，减小对队列和同路流的干扰。
- 在不同时间和不同帧上，交替使用更强/更弱的 burst，以区别“链路容量”和“背景流量占用”：
  - 当突发期间 RTT/到达间隔迅速上升，说明路径已经接近饱和；  
  - 当突发期间仍然没有明显 delay/丢包，说明还有未利用的空间。

通过统计一段时间内**每帧的发送时间窗口、大小与反馈信号**（delay/jitter/loss），Bandwidth Sampler 为后续模型提供：  
**对“可用带宽 A”与“背景 traffic 占用 B”的联合估计**。

### 4.2 帧大小模型：高斯分布下的 bit-rate variation

论文中强调：在 RTC 中，即使目标平均码率固定，实际编码帧大小 \\(S\\) 也会围绕目标发生大幅波动。BurstRTC 采用：

\\[
S \\sim \\mathcal{N}(\\mu_S, \\sigma_S^2)
\\]

这一假设的意义在于：

- 不再只看 \\(\\mu_S\\)（平均大小），而是明确考虑**方差 \\(\\sigma_S^2\\) 和 tail**；  
- 在给定链路带宽估计 A 时，如果直接设置 target bitrate \\(R = A\\)，那么由于 \\(S\\) 的波动，**高于均值的那些大帧会频繁拥塞队列**。

因此 BurstRTC 在解析速率控制时，会把以下约束显式纳入设计：

- 对于未来帧 \\(S\\) 的大小分布，  
- 要保证 \\(P(\\text{frame delay} > D_{\\text{target}})\\) 足够小；  
- 这相当于要求“在带宽 A 下，考虑 \\(\\sigma_S\\) 的情况下，队列不会因为偶发大帧而长期堆积”。

### 4.3 帧延迟模型：从带宽与帧分布推导 delay

帧延迟由多部分组成：

1. 编码延迟（encoder latency，受 codec 实现与 rate control interval 影响）；  
2. 发送排队与传输延迟（取决于可用带宽 A、背景流量、帧大小分布）；  
3. 接收端解码与播放缓冲延迟。

BurstRTC 的 frame delay model 重点建模的是**网络与排队部分**：

- 在给定 Bandwidth Sampler 提供的估计（链路服务速率分布）和帧到达过程（每帧的 burst + pacing 发送模式）下，  
- 利用排队论和统计近似，推导 future frame 的 delay 分布或其分位数上界；
- 其中显式考虑：
  - 编码器 rate control interval 导致的码率调节粒度；  
  - 网络反馈延迟（从测到拥塞到调整 target bitrate 的时滞）；  
  - 背景 traffic 对可用服务速率的侵占。

与 NDTC 的 FDACE 不同，BurstRTC 更倾向于：

- 使用 **概率描述**（分布、分位数）而非单一 A_n 值；  
- 在 delay 模型中既考虑 mean，也强调 tail 的行为。

### 4.4 解析速率控制（Analytic Rate Control）

传统拥塞控制（包括某些 RTC CC）往往采用：

- AIMD（加性增、乘性减）；  
- 或基于梯度的迭代更新（例如 Netflix 的某些自适应算法）。

这些方法的共同点是：  
**观测一次 → 小步调整参数 → 再观测 → 再调整**，收敛速度受步长和噪声影响较大。

BurstRTC 的解析速率控制试图：

- 在 Bandwidth Sampler、Frame Size Model、Frame Delay Model 都给出解析形式的前提下，  
- 构造一个优化目标：

  > 在 delay 约束（如 \\(P(\\text{delay} > D_{\\text{target}}) \\le \\epsilon\\)）下，最大化平均视频 bit-rate 或视觉质量指标。

- 然后通过公式和算法 **直接解出或近似出下一时刻的 target bitrate**：
  - target bitrate 低于链路估计容量，但考虑了 \\(\\sigma_S\\) 和 tail delay 风险；
  - 相比纯 AIMD，在带宽上升/下降时能更快地跳到相对合理的区间。

这种解法的直觉可以类比为：

- GCC/BBR：在“盲调一个控制参数”，看效果如何再调；  
- BurstRTC：在已有的统计模型上做一次“规划”，直接挑选一个在当前环境下看起来合理的目标点。

---

## 5. 优缺点与 BurstRTC 的优越性

### 5.1 优点

1. **显式处理 bit-rate variation 与帧级突发**  
   - 不再假设编码输出平滑，而是把帧大小波动当作一等控制对象；  
   - 通过帧大小高斯模型和 delay 模型控制 tail delay，特别适合对延迟敏感的交互式应用。

2. **统一控制回路（编码/传输一体）**  
   - 网络反馈直接决定视频 target bitrate，而不是“先估带宽、再让编码器自己做 rate control”；  
   - 避免双环路容易出现的“网络刚放松、编码器也加码，导致瞬时 overshoot”的问题。

3. **解析速率控制带来的快速收敛**  
   - 相较于 purely AIMD 或 gradient-based 调参，BurstRTC 能在更少的反馈周期内跳到合理的码率区间；  
   - 更适应蜂窝/无线等带宽高度波动的链路。

4. **实验结果上的显著优势**  
   - 论文在多种 trace 下展示：  
     - 相比 GCC，BurstRTC 在带宽利用率上最高可提升约 59.8%；  
     - 在平均帧延迟上可降低约 48.9%；  
     - 相比 SQP 和 Pudica，也能在 tail delay（如 95%/99% 延迟）和平均 bit-rate 上取得优势。

### 5.2 不足与挑战

1. **模型假设的准确性**  
   - 帧大小是否始终近似高斯分布，在不同内容/编码配置下可能有偏差；  
   - 帧延迟模型依赖对网络与背景 traffic 的建模，真实互联网中的多路径、多队列场景会更复杂。

2. **实现复杂度较高**  
   - 需要在编码器和传输栈中增加多处统计与控制逻辑；  
   - 需要在发送侧精准控制“burst + pacing”的发包模式；
   - 在生产环境中部署需要与现有 RTC 协议栈深度集成。

3. **公平性与共存问题**  
   - 帧突发本身在不当配置下可能对同路的 TCP/QUIC 产生冲击；  
   - 虽然论文通过 alternating burst/pacing 和更精细的 bandwidth sampling 来缓解，但在复杂多租户网络中仍需谨慎调参和公平性分析。

4. **与现有标准/实现的兼容性**  
   - 类似于 Salsify，完整的 BurstRTC 可能需要对现有 WebRTC 实现做较大改动，这对浏览器和移动端生态而言是一个门槛。

---

## 6. 在 `network-ws` 环境中的工程实现方案（预案）

> 本节给出在你当前 `network-ws`（Pion WebRTC + FFmpeg + mahimahi + FFmpeg 评估）环境下实现“工程近似版 BurstRTC”的设计草图，不直接写代码。
> 设计目标：让 BurstRTC‑like 实验路径可以与现有 GCC / NDTC / Salsify 一样，通过统一脚本完成“传输 → 修复 → 评估”流程。

### 6.1 Server 端：BurstRTC 控制器与发送逻辑

#### 6.1.1 新的 server 程序骨架

- 在 `src/` 下新增：
  - `server_burstrtc.go`：BurstRTC server 主程序，build tag `//go:build !js && burstrtc`；  
  - `burstrtc_controller.go`：封装 Bandwidth Sampler + Frame Size Model + Frame Delay Model + Analytic Rate Control；  
  - 若需要 FFmpeg 复用，则类似 `server_ffmpeg_gcc.go` / `server_ffmpeg_ndtc.go`，增加 `server_ffmpeg_burstrtc.go`。

- `server_burstrtc.go` 结构基本仿照 `server_ndtc.go` / `server_salsify.go`：
  - 解析参数：`-video/-ip/-offer-file/-answer-file/-loop/-session-dir`，可选 `-burstrtc-delay-target` 等；  
  - 使用 Pion `PeerConnection` 搭建 WebRTC 会话；  
  - 使用 FFmpeg 初始化解码/编码管线；  
  - 创建 `BurstRtcController` 实例，用于 per-frame 决策；  
  - 启动 `writeVideoToTrackBurstRTC(...)` 作为发送循环。

#### 6.1.2 `BurstRtcController` 设计

`BurstRtcController` 可以包含：

- **统计状态**：
  - 近期帧的大小样本集 \\(\\{S_n\\}\\)（用于估计 \\(\\mu_S, \\sigma_S\\)）；  
  - 近期帧的发送时间窗口（burst 部分、pacing 部分）与观测到的 delay/loss；  
  - 来自 client 的反馈（可选）。

- **模型参数**（可配置）：
  - 目标帧周期 \\(T_F\\)（例如 1/30s）；  
  - 目标 delay 上限 \\(D_{\\text{target}}\\) 和 tail 约束 \\(\\epsilon\\)；  
  - 窗口大小 N、平滑系数等。

- **核心方法**：
  - `UpdateSamples(observation)`：更新最近的带宽/帧大小/延迟样本；  
  - `EstimateCapacity()`：给出可用带宽 A 及背景 traffic 估计；  
  - `EstimateFrameStats()`：给出 \\(\\mu_S, \\sigma_S\\) 等帧大小统计；  
  - `NextFrameBudget() (targetBits int, burstFraction float64)`：在考虑 delay 约束后，解析或近似给出下一帧的目标 bit 数和适合的 burst 比例。

#### 6.1.3 发送逻辑：burst + pacing 的实现

在 `writeVideoToTrackBurstRTC` 中：

1. **解码一帧、编码为 H.264**  
   - 使用 FFmpeg 解码原始视频帧，再编码为 H.264 NAL 单元；  
   - 收集本帧的总 bit 数（编码前先按 controller 的 `targetBits` 调整 encoder 配置或选择合适的 CRF/QP）。

2. **拆分为 burst 部分与 pacing 部分**  
   - 根据 `burstFraction` 决定本帧中多少比例的 bit 在 burst 相中发送：  
     - 例如 50%–70% 的数据在很短时间窗口内集中发送（形成“微型 frame train”）；  
     - 剩余部分在帧周期内按照 estimated safe rate pacing。

3. **记录样本并更新控制器**  
   - 记录本帧的：
     - 编码大小 S_n；  
     - burst 阶段持续时间、pacing 阶段持续时间；  
     - RTT/到达反馈中的 delay/loss 指标（如从 client 回传或由 RTCP 间接推断）。  
   - 调用 `BurstRtcController.UpdateSamples()` 更新模型，为下一帧决策提供基础。

4. **日志与 CSV 输出**  
   - 在 `session_burstrtc_*` 目录下创建 `burstrtc_server_metrics.csv`，记录每帧的：  
     - `frame_index, target_bits, actual_bits, burst_fraction, send_start, send_end, est_capacity, est_delay_quantile,...`  
   - 方便与 GCC/NDTC/Salsify 的实验做统一对比。

### 6.2 Client 端：统计与反馈

在 client 侧，可以复用 NDTC/Salsify 客户端的结构，新增一个 `client_burstrtc.go`：

- build tag：`//go:build !js && burstrtc`；  
- 参数与 `client-gcc.go` / `client_ndtc.go` 对齐：`-offer-file/-answer-file/-output/-session-dir/-max-duration/-max-size`。

在 `OnTrack` 里：

1. **构建 per-frame 统计**  
   - 在 `writeH264ToFile` 之外，增加一层逻辑将 RTP 包按帧聚类：  
     - 记录每一帧的第一包/最后一包到达时间（接收持续时间 R_n）；  
     - 估计丢包（根据序列号 gap 或 RTCP NACK）；  
     - 如有必要，估计播放缓冲长度。

2. **输出客户端指标 CSV**  
   - 在 `session_burstrtc_*` 下创建 `burstrtc_client_metrics.csv`，记录：  
     - `frame_index, recv_start, recv_end, recv_duration, loss_flag, jitter,...`。

3. **可选：回传统计信息**  
   - 使用 WebRTC DataChannel 或自定义 RTCP 报文定期将部分统计发送给 server：  
     - 例如汇总窗口内的平均 `recv_duration`、jitter、loss rate；  
     - 供 Bandwidth Sampler 和 Frame Delay Model 使用。

### 6.3 脚本与评估：与现有 pipeline 对齐

在 `scripts/` 下新增：

- `server-burstrtc.sh`：  
  - 参数与 `server-gcc.sh` / `server-ndtc.sh` 一致（`--video/--ip/--session/--loop/--mmdelay/--mmloss/--mmlink`）；  
  - 构建命令：  
    ```bash
    go build -v -tags burstrtc -o build/server-burstrtc \
      src/server_burstrtc.go src/common.go src/burstrtc_controller.go src/server_ffmpeg_burstrtc.go
    ```  
  - 自动创建 `session_burstrtc_YYMMDDHHMM` 目录，放置 SDP 文件和 metrics CSV；  
  - 使用经过你验证的 mahimahi 命令拼接方式（`mm-delay`/`mm-loss` 前缀，`mm-link` 外层带 `--`）。

- `client-burstrtc.sh`：  
  - 参数与 `client-gcc.sh` / `client-ndtc.sh` 一致（`--video/--session/--ip/--max-duration/--max-size`）；  
  - 构建命令：  
    ```bash
    go build -v -tags burstrtc -o build/client-burstrtc \
      src/client_burstrtc.go src/common.go src/metrics.go src/burstrtc_controller.go src/h264_writer.go
    ```  
  - 若未指定 `--session`，自动选择最新的 `session_burstrtc_*` 目录；  
  - 运行完成后调用：
    ```bash
    ./scripts/evaluate.sh "$SESSION_DIR/received.h264" "$VIDEO_FILE" 30
    ```
    输出 PSNR/SSIM/VMAF 等指标。

### 6.4 与 GCC / NDTC / Salsify 的对比实验

完成上述工程近似实现后，可以在同一 trace、同一源视频上对比：

- **带宽利用率**：平均码率、峰值码率；  
- **时延分布**：average / P95 / P99 frame delay；  
- **质量指标**：PSNR/SSIM/VMAF 的时间序列与分布；  
- **自诱丢包与重传比例**。

这将为你提供一个统一框架下的对比视角：

- GCC：网络导向 + delay gradient；  
- NDTC：基于 FDACE 的“按时交付”控制；  
- Salsify：codec–transport 紧耦合的 per-frame 预算；  
- BurstRTC：以帧突发和统计模型为中心、解析速率控制的 CC。

---

## 7. 小结

BurstRTC 代表了一类“面向 RTC bit-rate variation”的新型拥塞控制思路：  
通过帧突发发送、帧大小统计建模、帧延迟解析建模以及解析速率控制，将网络拥塞控制与视频编码决策紧密结合，显式优化 tail delay 与带宽利用率。

在你的 `network-ws` 环境中，BurstRTC 可以作为 GCC / NDTC / Salsify 之外的又一重要对比对象，帮助系统性理解“按帧控制 + 编码/传输一体化”在低延迟视频中的潜力与代价。


