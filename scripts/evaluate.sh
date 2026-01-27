#!/bin/bash
# Video quality evaluation script
# Usage: ./evaluate.sh <received.h264> <reference.mp4> [fps]

set -e

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

# Convert received H264 to MP4
RECEIVED_MP4="${RECEIVED%.*}.mp4"
echo "[1/4] Converting received.h264 to MP4..."
ffmpeg -y -fflags +genpts -r "$FPS" -i "$RECEIVED" -c:v copy "$RECEIVED_MP4" 2>/dev/null
echo "  ✓ Converted to: $RECEIVED_MP4"
echo ""

# Calculate PSNR
echo "[2/4] Calculating PSNR..."
PSNR_LOG="${RECEIVED%.*}_psnr.log"
ffmpeg -i "$RECEIVED_MP4" -i "$REFERENCE" \
  -lavfi "psnr=stats_file=$PSNR_LOG" \
  -f null - 2>&1 | grep -E "average:|PSNR" || echo "  PSNR calculation completed"
echo "  ✓ Results saved to: $PSNR_LOG"
echo ""

# Calculate SSIM
echo "[3/4] Calculating SSIM..."
SSIM_LOG="${RECEIVED%.*}_ssim.log"
ffmpeg -i "$RECEIVED_MP4" -i "$REFERENCE" \
  -lavfi "ssim=stats_file=$SSIM_LOG" \
  -f null - 2>&1 | grep -E "SSIM|All:" || echo "  SSIM calculation completed"
echo "  ✓ Results saved to: $SSIM_LOG"
echo ""

# Calculate VMAF (if available)
echo "[4/4] Calculating VMAF..."
VMAF_JSON="${RECEIVED%.*}_vmaf.json"
if ffmpeg -i "$RECEIVED_MP4" -i "$REFERENCE" \
  -lavfi "libvmaf=log_path=$VMAF_JSON:log_fmt=json" \
  -f null - 2>&1 | grep -E "VMAF|libvmaf"; then
    echo "  ✓ Results saved to: $VMAF_JSON"
else
    echo "  ⚠ VMAF calculation failed (libvmaf may not be available)"
    echo "  Install libvmaf or use PSNR/SSIM instead"
fi
echo ""

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

