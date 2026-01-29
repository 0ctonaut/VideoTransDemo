#!/bin/bash
# WebRTC Server startup script
# Usage: ./server.sh -video <video_file> [-ip <ip_address>]

set -e

# Parse arguments
VIDEO_FILE=""
SERVER_IP=""
LOOP_VIDEO=""
USE_NETNS=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -video)
            VIDEO_FILE="$2"
            shift 2
            ;;
        -ip)
            SERVER_IP="$2"
            shift 2
            ;;
        -loop)
            LOOP_VIDEO="-loop"
            shift
            ;;
        -netns|--use-netns)
            USE_NETNS="yes"
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 -video <video_file> [-ip <ip_address>] [-loop] [-netns]"
            exit 1
            ;;
    esac
done

# Check if video file is provided
if [ -z "$VIDEO_FILE" ]; then
    echo "Error: -video parameter is required"
    echo "Usage: $0 -video <video_file> [-ip <ip_address>]"
    exit 1
fi

# Check if video file exists
if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Video file not found: $VIDEO_FILE"
    exit 1
fi

# Check and auto-build server if needed
# 如果二进制文件不存在，或者源文件比二进制文件新，则自动编译
SERVER_BIN="./build/server"
if [ ! -f "$SERVER_BIN" ] || [ "src/server.go" -nt "$SERVER_BIN" ] || [ "src/common.go" -nt "$SERVER_BIN" ]; then
    echo "Building server..."
    make server
    if [ $? -ne 0 ]; then
        echo "Error: Failed to build server"
        exit 1
    fi
fi

# Check if namespace exists when -netns is used
if [ -n "$USE_NETNS" ]; then
    if ! sudo ip netns list | grep -q "^server "; then
        echo "Error: 'server' network namespace not found. Please run ./run-setup.sh first."
        exit 1
    fi
    # Force use virtual network IP
    SERVER_IP="192.168.100.1"
    echo "Using network namespace: server"
    echo "Forced IP address: $SERVER_IP (virtual network)"
fi

# Create timestamp folder (format: yymmddhhmm)
TIMESTAMP=$(date +%y%m%d%H%M)
SESSION_DIR="session_${TIMESTAMP}"

# Create session directory
mkdir -p "$SESSION_DIR"
echo "=========================================="
echo "  WebRTC Server Session"
echo "=========================================="
echo "Session directory: $SESSION_DIR"
echo "Video file: $VIDEO_FILE"
if [ -n "$USE_NETNS" ]; then
    echo "Network namespace: server (virtual network)"
    echo "Server IP: $SERVER_IP (forced for virtual network)"
elif [ -n "$SERVER_IP" ]; then
    echo "Server IP: $SERVER_IP"
else
    echo "Server IP: auto-detect"
fi
if [ -n "$LOOP_VIDEO" ]; then
    echo "Loop mode: enabled (video will loop)"
else
    echo "Loop mode: disabled (video will play once)"
fi
echo ""

# Set file paths
OFFER_FILE="$SESSION_DIR/offer.txt"
ANSWER_FILE="$SESSION_DIR/answer.txt"

echo "Offer file: $OFFER_FILE"
echo "Answer file: $ANSWER_FILE"
echo ""
echo "Waiting for client to generate answer..."
echo ""

# Build server command
SERVER_CMD="./server -video \"$VIDEO_FILE\" -offer-file \"$OFFER_FILE\" -answer-file \"$ANSWER_FILE\""

if [ -n "$SERVER_IP" ]; then
    SERVER_CMD="$SERVER_CMD -ip \"$SERVER_IP\""
fi

if [ -n "$LOOP_VIDEO" ]; then
    SERVER_CMD="$SERVER_CMD -loop"
fi

# Wrap command with namespace if -netns is used
if [ -n "$USE_NETNS" ]; then
    # Use absolute path for binary and files to ensure they're accessible in namespace
    ABS_SERVER_BIN="$(pwd)/build/server"
    ABS_VIDEO_FILE="$(readlink -f "$VIDEO_FILE" 2>/dev/null || echo "$(pwd)/$VIDEO_FILE")"
    ABS_OFFER_FILE="$(pwd)/$OFFER_FILE"
    ABS_ANSWER_FILE="$(pwd)/$ANSWER_FILE"
    
    # Rebuild command with absolute paths
    SERVER_CMD="$ABS_SERVER_BIN -video \"$ABS_VIDEO_FILE\" -offer-file \"$ABS_OFFER_FILE\" -answer-file \"$ABS_ANSWER_FILE\" -ip \"$SERVER_IP\""
    if [ -n "$LOOP_VIDEO" ]; then
        SERVER_CMD="$SERVER_CMD -loop"
    fi
    
    # Wrap with namespace execution
    SERVER_CMD="sudo ip netns exec server $SERVER_CMD"
fi

# Run server
eval $SERVER_CMD

