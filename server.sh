#!/bin/bash
# WebRTC Server startup script
# Usage: ./server.sh -video <video_file> [-ip <ip_address>]

set -e

# Parse arguments
VIDEO_FILE=""
SERVER_IP=""
LOOP_VIDEO=""

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
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 -video <video_file> [-ip <ip_address>] [-loop]"
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

# Check if server binary exists
if [ ! -f "./server" ]; then
    echo "Error: server binary not found. Please build it first:"
    echo "  go build -o server server.go"
    exit 1
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
if [ -n "$SERVER_IP" ]; then
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

# Run server
eval $SERVER_CMD

