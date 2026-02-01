#!/bin/bash
# Salsify WebRTC Client script
#
# Usage:
#   ./scripts/client-salsify.sh --video assets/Ultra.mp4 [--session NAME] [--ip 192.168.100.2]
#                               [--max-duration 60s] [--max-size 200]
#
# 说明：
#   - 自动发现或指定 Salsify 实验 session 目录
#   - 通过 offer/answer 文件与 server-salsify 完成 SDP 握手
#   - 运行 client-salsify 接收 H.264 原始码流
#   - 调用 scripts/evaluate.sh 使用 FFmpeg 计算 PSNR/SSIM/VMAF

set -e

VIDEO_FILE=""
SESSION_NAME=""
CLIENT_IP=""
MAX_DURATION=""
MAX_SIZE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --video)
            VIDEO_FILE="$2"
            shift 2
            ;;
        --session)
            SESSION_NAME="$2"
            shift 2
            ;;
        --ip)
            CLIENT_IP="$2"
            shift 2
            ;;
        --max-duration)
            MAX_DURATION="$2"
            shift 2
            ;;
        --max-size)
            MAX_SIZE="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 --video <video_file> [--session NAME] [--ip <ip_address>] [--max-duration DURATION] [--max-size MB]"
            exit 1
            ;;
    esac
done

if [ -z "$VIDEO_FILE" ]; then
    echo "Error: --video parameter is required (reference video, e.g., assets/Ultra.mp4)"
    exit 1
fi

if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Reference video file not found: $VIDEO_FILE"
    exit 1
fi

# Session directory: 如果未指定，则选择最新的 session_salsify_* 目录
if [ -z "$SESSION_NAME" ]; then
    LATEST_SESSION=$(ls -td session_salsify_* 2>/dev/null | head -1 || true)
    if [ -z "$LATEST_SESSION" ]; then
        echo "Error: No session_salsify_* directory found. Please run server-salsify.sh first or specify --session."
        exit 1
    fi
    SESSION_DIR="$LATEST_SESSION"
else
    SESSION_DIR="$SESSION_NAME"
fi

if [ ! -d "$SESSION_DIR" ]; then
    echo "Error: Session directory not found: $SESSION_DIR"
    exit 1
fi

OFFER_FILE="$SESSION_DIR/offer.txt"
ANSWER_FILE="$SESSION_DIR/answer.txt"
OUTPUT_FILE="$SESSION_DIR/received.h264"

if [ ! -f "$OFFER_FILE" ]; then
    echo "Error: Offer file not found: $OFFER_FILE"
    echo "Make sure server-salsify.sh has been started and generated the offer."
    exit 1
fi

# Build Salsify client if needed
CLIENT_BIN="./build/client-salsify"
if [ ! -f "$CLIENT_BIN" ] || [ "src/client_salsify.go" -nt "$CLIENT_BIN" ] || [ "src/common.go" -nt "$CLIENT_BIN" ]; then
    echo "Building Salsify client..."
    mkdir -p build
    go build -v -tags salsify -o "$CLIENT_BIN" \
      src/client_salsify.go src/common.go src/metrics.go src/salsify_controller.go src/h264_writer.go src/metrics_summary.go src/frame_metadata.go
fi

echo "=========================================="
echo "  Salsify WebRTC Client Session"
echo "=========================================="
echo "Session directory: $SESSION_DIR"
echo "Offer file:  $OFFER_FILE"
echo "Answer file: $ANSWER_FILE"
echo "Output file: $OUTPUT_FILE"
if [ -n "$CLIENT_IP" ]; then
    echo "Client IP: $CLIENT_IP"
else
    echo "Client IP: auto-detect"
fi
echo ""

CLIENT_CMD="$CLIENT_BIN -offer-file \"$OFFER_FILE\" -answer-file \"$ANSWER_FILE\" -output \"$OUTPUT_FILE\" -session-dir \"$SESSION_DIR\""

if [ -n "$CLIENT_IP" ]; then
    CLIENT_CMD="$CLIENT_CMD -ip \"$CLIENT_IP\""
fi

if [ -n "$MAX_DURATION" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-duration \"$MAX_DURATION\""
fi

if [ -n "$MAX_SIZE" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-size \"$MAX_SIZE\""
fi

echo "Running client:"
echo "  $CLIENT_CMD"
echo ""

eval $CLIENT_CMD

echo ""
echo "Client finished. Starting FFmpeg-based evaluation..."

if [ ! -x "./scripts/evaluate.sh" ]; then
    echo "Error: scripts/evaluate.sh not found or not executable."
    exit 1
fi

./scripts/evaluate.sh "$OUTPUT_FILE" "$VIDEO_FILE" 30

echo "Evaluation finished. All results are under: $SESSION_DIR"