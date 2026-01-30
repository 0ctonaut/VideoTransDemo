#!/bin/bash
# BurstRTC WebRTC Server startup script with mahimahi support
#
# Usage:
#   ./scripts/server-burst.sh --video assets/Ultra.mp4 [--ip 192.168.100.1] [--session NAME] [--loop]
#                           [--mmdelay MS] [--mmloss UP_LOSS DOWN_LOSS] [--mmlink UPLINK DOWNLINK]
#
# 说明：
#   - 自动创建 session_burst_YYMMDDHHMM 目录（或使用 --session 指定的目录名）
#   - 通过 -offer-file/-answer-file 与 client-burst 使用文件交换 SDP
#   - 可选地在前面加上 mahimahi 命令：mmdelay / mmloss / mmlink

set -e

VIDEO_FILE=""
SERVER_IP=""
SESSION_NAME=""
LOOP_VIDEO=""
MM_DELAY=""
MM_LOSS_UP=""
MM_LOSS_DOWN=""
MM_LINK_UP=""
MM_LINK_DOWN=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --video)
            VIDEO_FILE="$2"
            shift 2
            ;;
        --ip)
            SERVER_IP="$2"
            shift 2
            ;;
        --session)
            SESSION_NAME="$2"
            shift 2
            ;;
        --loop)
            LOOP_VIDEO="yes"
            shift
            ;;
        --mmdelay)
            MM_DELAY="$2"
            shift 2
            ;;
        --mmloss)
            if [ $# -lt 3 ]; then
                echo "Error: --mmloss requires two arguments: <uplink_loss> <downlink_loss>"
                echo "Example: --mmloss 0.01 0.01"
                exit 1
            fi
            MM_LOSS_UP="$2"
            MM_LOSS_DOWN="$3"
            shift 3
            ;;
        --mmlink)
            # 期望：--mmlink uplink.downlink downlink.trace
            MM_LINK_UP="$2"
            MM_LINK_DOWN="$3"
            shift 3
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 --video <video_file> [--ip <ip_address>] [--session NAME] [--loop] [--mmdelay MS] [--mmloss UP_LOSS DOWN_LOSS] [--mmlink UPLINK DOWNLINK]"
            exit 1
            ;;
    esac
done

if [ -z "$VIDEO_FILE" ]; then
    echo "Error: --video parameter is required"
    exit 1
fi

if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Video file not found: $VIDEO_FILE"
    exit 1
fi

# Build BurstRTC server if needed
SERVER_BIN="./build/server-burst"
if [ ! -f "$SERVER_BIN" ] || [ "src/server_burst.go" -nt "$SERVER_BIN" ] || [ "src/common.go" -nt "$SERVER_BIN" ] || [ "src/burst_controller.go" -nt "$SERVER_BIN" ] || [ "src/server_ffmpeg_burst.go" -nt "$SERVER_BIN" ]; then
    echo "Building BurstRTC server..."
    mkdir -p build
    go build -v -tags burst -o "$SERVER_BIN" \
        src/server_burst.go src/common.go src/burst_controller.go src/server_ffmpeg_burst.go
fi

# Session directory: session_burst_YYMMDDHHMM or custom name
if [ -z "$SESSION_NAME" ]; then
    TIMESTAMP=$(date +%y%m%d%H%M)
    SESSION_DIR="session_burst_${TIMESTAMP}"
else
    SESSION_DIR="$SESSION_NAME"
fi

mkdir -p "$SESSION_DIR"

OFFER_FILE="$SESSION_DIR/offer.txt"
ANSWER_FILE="$SESSION_DIR/answer.txt"

echo "=========================================="
echo "  BurstRTC WebRTC Server Session"
echo "=========================================="
echo "Session directory: $SESSION_DIR"
echo "Video file: $VIDEO_FILE"
if [ -n "$SERVER_IP" ]; then
    echo "Server IP: $SERVER_IP"
else
    echo "Server IP: auto-detect"
fi
if [ -n "$LOOP_VIDEO" ]; then
    echo "Loop mode: enabled"
else
    echo "Loop mode: disabled"
fi
if [ -n "$MM_DELAY" ] || { [ -n "$MM_LOSS_UP" ] && [ -n "$MM_LOSS_DOWN" ]; } || { [ -n "$MM_LINK_UP" ] && [ -n "$MM_LINK_DOWN" ]; }; then
    echo "Mahimahi: enabled"
    [ -n "$MM_DELAY" ] && echo "  mm-delay: $MM_DELAY ms"
    if [ -n "$MM_LOSS_UP" ] && [ -n "$MM_LOSS_DOWN" ]; then
        echo "  mm-loss: up=$MM_LOSS_UP down=$MM_LOSS_DOWN"
    fi
    if [ -n "$MM_LINK_UP" ] && [ -n "$MM_LINK_DOWN" ]; then
        echo "  mm-link: $MM_LINK_UP $MM_LINK_DOWN"
    fi
else
    echo "Mahimahi: disabled"
fi
echo ""
echo "Offer file:  $OFFER_FILE"
echo "Answer file: $ANSWER_FILE"
echo ""

# Base server command
SERVER_CMD="$SERVER_BIN -video \"$VIDEO_FILE\" -offer-file \"$OFFER_FILE\" -answer-file \"$ANSWER_FILE\" -session-dir \"$SESSION_DIR\""

if [ -n "$SERVER_IP" ]; then
    SERVER_CMD="$SERVER_CMD -ip \"$SERVER_IP\""
fi

if [ -n "$LOOP_VIDEO" ]; then
    SERVER_CMD="$SERVER_CMD -loop"
fi

# Mahimahi wrapping（注意：只有 mm-link 使用 --，mm-delay/mm-loss 直接前缀命令）
FULL_CMD="$SERVER_CMD"

# 先包下行丢包，再包上行丢包，再包延迟，最后再包 mm-link
if [ -n "$MM_LOSS_DOWN" ]; then
    FULL_CMD="mm-loss downlink $MM_LOSS_DOWN $FULL_CMD"
fi

if [ -n "$MM_LOSS_UP" ]; then
    FULL_CMD="mm-loss uplink $MM_LOSS_UP $FULL_CMD"
fi

if [ -n "$MM_DELAY" ]; then
    FULL_CMD="mm-delay $MM_DELAY $FULL_CMD"
fi

if [ -n "$MM_LINK_UP" ] && [ -n "$MM_LINK_DOWN" ]; then
    FULL_CMD="mm-link $MM_LINK_UP $MM_LINK_DOWN -- $FULL_CMD"
fi

echo "Running command:"
echo "  $FULL_CMD"
echo ""

eval $FULL_CMD



