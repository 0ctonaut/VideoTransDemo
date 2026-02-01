#!/bin/bash
# Video quality evaluation script
# Usage: ./evaluate.sh <received.h264> <reference.mp4> [fps]

set -e

# Prefer user-compiled ffmpeg-vmaf by default, but allow override / fallback.
DEFAULT_FFMPEG="$HOME/ffmpeg-vmaf/bin/ffmpeg"
if [ -x "$DEFAULT_FFMPEG" ]; then
    FFMPEG_BIN="${FFMPEG_BIN:-$DEFAULT_FFMPEG}"
    # Set LD_LIBRARY_PATH for custom ffmpeg-vmaf binary
    # Ensure the path exists and is properly formatted
    if [ -d "$HOME/ffmpeg-vmaf/lib" ]; then
        if [ -z "$LD_LIBRARY_PATH" ]; then
            export LD_LIBRARY_PATH="$HOME/ffmpeg-vmaf/lib"
        else
            export LD_LIBRARY_PATH="$HOME/ffmpeg-vmaf/lib:$LD_LIBRARY_PATH"
        fi
    fi
else
    FFMPEG_BIN="${FFMPEG_BIN:-ffmpeg}"
fi

RECEIVED="$1"
REFERENCE="$2"
FPS="${3:-30}"

if [ -z "$RECEIVED" ] || [ -z "$REFERENCE" ]; then
    echo "Usage: $0 <received.h264> <reference.mp4> [fps]"
    echo "  Example: $0 session_2501151430/received.h264 Ultra.mp4 30"
    exit 1
fi

if [ ! -f "$RECEIVED" ]; then
    echo "Error: Received file not found: $RECEIVED"
    exit 1
fi

if [ ! -f "$REFERENCE" ]; then
    echo "Error: Reference file not found: $REFERENCE"
    exit 1
fi

echo "=========================================="
echo "  Video Quality Evaluation"
echo "=========================================="
echo "Received file: $RECEIVED"
echo "Reference file: $REFERENCE"
echo "Frame rate: $FPS fps"
echo ""

# Convert received H264 to MP4 (robust re-encode)
RECEIVED_MP4="${RECEIVED%.*}.mp4"
echo "[1/4] Converting received.h264 to MP4..."
echo "  Using ffmpeg binary: $FFMPEG_BIN"
if ! "$FFMPEG_BIN" -y -fflags +genpts -err_detect ignore_err -r "$FPS" -i "$RECEIVED" \
  -c:v libx264 -preset slow -crf 18 "$RECEIVED_MP4"; then
  echo "  ✗ FFmpeg convert failed (step 1/4). See error log above."
  exit 1
fi
echo "  ✓ Converted to: $RECEIVED_MP4"
echo ""

# Calculate PSNR
echo "[2/4] Calculating PSNR..."
PSNR_LOG="${RECEIVED%.*}_psnr.log"
"$FFMPEG_BIN" -i "$RECEIVED_MP4" -i "$REFERENCE" \
  -lavfi "psnr=stats_file=$PSNR_LOG" \
  -f null - 2>&1 | grep -E "average:|PSNR" || echo "  PSNR calculation completed"
echo "  ✓ Results saved to: $PSNR_LOG"
echo ""

# Calculate SSIM
echo "[3/4] Calculating SSIM..."
SSIM_LOG="${RECEIVED%.*}_ssim.log"
"$FFMPEG_BIN" -i "$RECEIVED_MP4" -i "$REFERENCE" \
  -lavfi "ssim=stats_file=$SSIM_LOG" \
  -f null - 2>&1 | grep -E "SSIM|All:" || echo "  SSIM calculation completed"
echo "  ✓ Results saved to: $SSIM_LOG"
echo ""

# Calculate VMAF (if available)
echo "[4/4] Calculating VMAF..."
VMAF_JSON="${RECEIVED%.*}_vmaf.json"

# Default VMAF model path for user-compiled ffmpeg-vmaf; can be overridden.
VMAF_MODEL="${VMAF_MODEL:-$HOME/ffmpeg-vmaf/share/model/vmaf_v0.6.1.json}"

if [ -f "$VMAF_MODEL" ]; then
    VMAF_FILTER="libvmaf=log_fmt=json:log_path=$VMAF_JSON:model_path=$VMAF_MODEL"
else
    echo "  ⚠ VMAF model not found at $VMAF_MODEL, using libvmaf default model path"
    VMAF_FILTER="libvmaf=log_fmt=json:log_path=$VMAF_JSON"
fi

# Follow Netflix documentation: set frame rate before -i, reset PTS before libvmaf
if "$FFMPEG_BIN" -r "$FPS" -i "$REFERENCE" -r "$FPS" -i "$RECEIVED_MP4" \
  -lavfi "[0:v]setpts=PTS-STARTPTS[reference]; \
          [1:v]setpts=PTS-STARTPTS[distorted]; \
          [distorted][reference]$VMAF_FILTER" \
  -f null - 2>&1 | grep -E "VMAF|libvmaf"; then
    echo "  ✓ Results saved to: $VMAF_JSON"
else
    echo "  ⚠ VMAF calculation failed (libvmaf may not be available or model missing)"
    echo "  Install libvmaf-enabled ffmpeg or use PSNR/SSIM instead"
fi
echo ""

# 显示汇总统计（如果存在）
SUMMARY_JSON="${SESSION_DIR}/metrics_summary.json"
if [ -f "$SUMMARY_JSON" ]; then
    echo ""
    echo "=========================================="
    echo "  Frame Metrics Summary"
    echo "=========================================="
    if command -v jq &> /dev/null; then
        # 使用 jq 格式化输出
        jq -r '
            "Total Frames:           \(.total_frames)",
            "Average Latency:        \(.average_latency_ms) ms",
            "P99 Latency:            \(.p99_latency_ms) ms",
            "Stall Rate:             \(.stall_rate * 100) % (\(.total_stall_frames) frames)",
            "Effective Bitrate:      \(.effective_bitrate_kbps) kbps",
            "Total Duration:         \(.total_duration_seconds) seconds"
        ' "$SUMMARY_JSON"
    else
        # 如果没有 jq，直接显示文本文件
        SUMMARY_TXT="${SESSION_DIR}/metrics_summary.txt"
        if [ -f "$SUMMARY_TXT" ]; then
            cat "$SUMMARY_TXT"
        fi
    fi
    echo ""
fi

echo "=========================================="
echo "  Evaluation Complete"
echo "=========================================="
echo "Results:"
echo "  - PSNR: $PSNR_LOG"
echo "  - SSIM: $SSIM_LOG"
if [ -f "$VMAF_JSON" ]; then
    echo "  - VMAF: $VMAF_JSON"
    # Extract average VMAF score
    if command -v jq &> /dev/null; then
        AVG_VMAF=$(jq -r '.pooled_metrics.vmaf.harmonic_mean' "$VMAF_JSON" 2>/dev/null || echo "N/A")
        echo "  - Average VMAF: $AVG_VMAF"
    fi
fi
echo ""
