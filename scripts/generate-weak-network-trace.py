#!/usr/bin/env python3
"""
生成弱网环境 trace 文件（用于 mahimahi mm-link）

生成 500kbps ~ 2000kbps 浮动的带宽 trace 文件。

Trace 文件格式：
- 每行代表一个 packet delivery opportunity
- 每个机会 = 1500 字节（MTU）
- 数字表示时间戳（毫秒）

计算方式：
- 目标带宽: 500-2000 kbps = 62.5-250 KB/s = 41.67-166.67 packets/s
- 时间间隔: 1000ms / packets_per_sec = 6-24 ms per packet
- 添加随机波动（±20%）
"""

import random
import sys
import os

def generate_trace_file(output_path, duration_seconds=300, min_kbps=500, max_kbps=2000, variation=0.2):
    """
    生成 trace 文件
    
    参数:
        output_path: 输出文件路径
        duration_seconds: trace 持续时间（秒）
        min_kbps: 最小带宽（kbps）
        max_kbps: 最大带宽（kbps）
        variation: 随机波动范围（±20% = 0.2）
    """
    MTU_BYTES = 1500  # MTU 大小（字节）
    MTU_BITS = MTU_BYTES * 8  # MTU 大小（比特）
    
    # 计算平均带宽（kbps）
    avg_kbps = (min_kbps + max_kbps) / 2.0
    
    # 生成时间戳列表
    timestamps = []
    current_time_ms = 0
    
    # 使用正弦波 + 随机波动来模拟带宽变化
    import math
    
    while current_time_ms < duration_seconds * 1000:
        # 使用正弦波在 min 和 max 之间波动
        # 周期约为 30 秒
        cycle_position = (current_time_ms / 1000.0) % 30.0
        sine_value = math.sin(2 * math.pi * cycle_position / 30.0)
        # 将正弦值映射到 [min_kbps, max_kbps] 范围
        target_kbps = min_kbps + (max_kbps - min_kbps) * (sine_value + 1) / 2.0
        
        # 添加随机波动（±variation）
        random_factor = 1.0 + random.uniform(-variation, variation)
        current_kbps = target_kbps * random_factor
        # 确保在范围内
        current_kbps = max(min_kbps, min(max_kbps, current_kbps))
        
        # 计算每个 packet 的时间间隔（毫秒）
        # packets_per_sec = (current_kbps * 1000) / MTU_BITS
        # interval_ms = 1000 / packets_per_sec = MTU_BITS / current_kbps
        interval_ms = (MTU_BITS * 1000.0) / (current_kbps * 1000.0)
        
        # 添加一些随机性到时间间隔（±10%）
        interval_ms *= (1.0 + random.uniform(-0.1, 0.1))
        
        # 确保间隔至少为 1ms
        interval_ms = max(1.0, interval_ms)
        
        # 记录时间戳
        timestamps.append(int(current_time_ms))
        
        # 更新当前时间
        current_time_ms += interval_ms
    
    # 写入文件
    with open(output_path, 'w') as f:
        for ts in timestamps:
            f.write(f"{ts}\n")
    
    print(f"Generated trace file: {output_path}")
    print(f"  Duration: {duration_seconds} seconds")
    print(f"  Bandwidth range: {min_kbps}-{max_kbps} kbps")
    print(f"  Total opportunities: {len(timestamps)}")
    print(f"  Average interval: {timestamps[-1] / len(timestamps):.2f} ms")
    
    return timestamps

def main():
    # 默认参数
    duration = 300  # 5 分钟
    min_kbps = 500
    max_kbps = 2000
    variation = 0.2  # ±20%
    
    # 解析命令行参数
    if len(sys.argv) > 1:
        if sys.argv[1] in ['-h', '--help']:
            print("Usage: generate-weak-network-trace.py [output_dir] [duration_seconds] [min_kbps] [max_kbps]")
            print("  output_dir: 输出目录（默认: ~/mahimahi/traces）")
            print("  duration_seconds: trace 持续时间（默认: 300）")
            print("  min_kbps: 最小带宽（默认: 500）")
            print("  max_kbps: 最大带宽（默认: 2000）")
            sys.exit(0)
    
    output_dir = os.path.expanduser("~/mahimahi/traces")
    if len(sys.argv) > 1:
        output_dir = sys.argv[1]
    if len(sys.argv) > 2:
        duration = int(sys.argv[2])
    if len(sys.argv) > 3:
        min_kbps = int(sys.argv[3])
    if len(sys.argv) > 4:
        max_kbps = int(sys.argv[4])
    
    # 确保输出目录存在
    os.makedirs(output_dir, exist_ok=True)
    
    # 生成 uplink 和 downlink trace 文件
    uplink_path = os.path.join(output_dir, "weak-network-500-2000kbps.up")
    downlink_path = os.path.join(output_dir, "weak-network-500-2000kbps.down")
    
    print("Generating weak network trace files...")
    print(f"  Output directory: {output_dir}")
    print(f"  Duration: {duration} seconds")
    print(f"  Bandwidth range: {min_kbps}-{max_kbps} kbps")
    print()
    
    # 生成 uplink（使用不同的随机种子以产生不同的模式）
    random.seed(42)
    generate_trace_file(uplink_path, duration, min_kbps, max_kbps, variation)
    
    # 生成 downlink（使用不同的随机种子）
    random.seed(123)
    generate_trace_file(downlink_path, duration, min_kbps, max_kbps, variation)
    
    print()
    print("Trace files generated successfully!")
    print(f"  Uplink: {uplink_path}")
    print(f"  Downlink: {downlink_path}")
    print()
    print("Usage example:")
    print(f"  ./scripts/server-gcc.sh --video assets/Ultra.mp4 --mmlink {uplink_path} {downlink_path}")

if __name__ == "__main__":
    main()

