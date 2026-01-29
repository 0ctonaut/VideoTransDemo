#!/bin/bash
# WebRTC Client startup script
# Usage: ./client.sh [session_folder] [-ip <ip_address>] [-output <output_file>]

set -e

# Parse arguments
SESSION_DIR=""
CLIENT_IP=""
OUTPUT_FILE=""
MAX_DURATION=""
MAX_SIZE=""
USE_NETNS=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -ip)
            CLIENT_IP="$2"
            shift 2
            ;;
        -output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        -max-duration)
            MAX_DURATION="$2"
            shift 2
            ;;
        -max-size)
            MAX_SIZE="$2"
            shift 2
            ;;
        -netns|--use-netns)
            USE_NETNS="yes"
            shift
            ;;
        -*)
            echo "Unknown option: $1"
            echo "Usage: $0 [session_folder] [-ip <ip_address>] [-output <output_file>] [-max-duration <duration>] [-max-size <MB>] [-netns]"
            exit 1
            ;;
        *)
            # First non-option argument is session folder
            if [ -z "$SESSION_DIR" ]; then
                SESSION_DIR="$1"
            else
                echo "Error: Multiple session folders specified"
                exit 1
            fi
            shift
            ;;
    esac
done

# If session folder not specified, find the latest one
if [ -z "$SESSION_DIR" ]; then
    LATEST_SESSION=$(ls -td session_* 2>/dev/null | head -1)
    if [ -z "$LATEST_SESSION" ]; then
        echo "Error: No session folder found. Please specify a session folder or run server.sh first."
        echo "Usage: $0 [session_folder] [-ip <ip_address>] [-output <output_file>]"
        exit 1
    fi
    SESSION_DIR="$LATEST_SESSION"
    echo "Auto-detected latest session folder: $SESSION_DIR"
fi

# Check if session directory exists
if [ ! -d "$SESSION_DIR" ]; then
    echo "Error: Session directory not found: $SESSION_DIR"
    exit 1
fi

# Check and auto-build client if needed
# 如果二进制文件不存在，或者源文件比二进制文件新，则自动编译
CLIENT_BIN="./build/client"
if [ ! -f "$CLIENT_BIN" ] || [ "src/client.go" -nt "$CLIENT_BIN" ] || [ "src/common.go" -nt "$CLIENT_BIN" ]; then
    echo "Building client..."
    make client
    if [ $? -ne 0 ]; then
        echo "Error: Failed to build client"
        exit 1
    fi
fi

# Check if namespace exists when -netns is used
if [ -n "$USE_NETNS" ]; then
    if ! sudo ip netns list | grep -q "^client "; then
        echo "Error: 'client' network namespace not found. Please run ./run-setup.sh first."
        exit 1
    fi
    # Force use virtual network IP
    CLIENT_IP="192.168.100.2"
    echo "Using network namespace: client"
    echo "Forced IP address: $CLIENT_IP (virtual network)"
fi

# Set file paths
OFFER_FILE="$SESSION_DIR/offer.txt"
ANSWER_FILE="$SESSION_DIR/answer.txt"

# Check if offer file exists
if [ ! -f "$OFFER_FILE" ]; then
    echo "Error: Offer file not found: $OFFER_FILE"
    echo "Please make sure server.sh has been run first."
    exit 1
fi

# Set default output file if not specified
if [ -z "$OUTPUT_FILE" ]; then
    OUTPUT_FILE="$SESSION_DIR/received.h264"
fi

echo "=========================================="
echo "  WebRTC Client Session"
echo "=========================================="
echo "Session directory: $SESSION_DIR"
echo "Offer file: $OFFER_FILE"
echo "Answer file: $ANSWER_FILE"
echo "Output file: $OUTPUT_FILE"
if [ -n "$USE_NETNS" ]; then
    echo "Network namespace: client (virtual network)"
    echo "Client IP: $CLIENT_IP (forced for virtual network)"
elif [ -n "$CLIENT_IP" ]; then
    echo "Client IP: $CLIENT_IP"
else
    echo "Client IP: auto-detect"
fi
echo ""

# Build client command
CLIENT_CMD="./client -answer-file \"$ANSWER_FILE\" -output \"$OUTPUT_FILE\""

if [ -n "$CLIENT_IP" ]; then
    CLIENT_CMD="$CLIENT_CMD -ip \"$CLIENT_IP\""
fi

if [ -n "$MAX_DURATION" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-duration \"$MAX_DURATION\""
fi

if [ -n "$MAX_SIZE" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-size \"$MAX_SIZE\""
fi

# Wrap command with namespace if -netns is used
if [ -n "$USE_NETNS" ]; then
    # Use absolute paths for binary and files to ensure they're accessible in namespace
    ABS_CLIENT_BIN="$(pwd)/build/client"
    ABS_ANSWER_FILE="$(pwd)/$ANSWER_FILE"
    ABS_OUTPUT_FILE="$(pwd)/$OUTPUT_FILE"
    
    # Rebuild command with absolute paths
    CLIENT_CMD="$ABS_CLIENT_BIN -answer-file \"$ABS_ANSWER_FILE\" -output \"$ABS_OUTPUT_FILE\" -ip \"$CLIENT_IP\""
    if [ -n "$MAX_DURATION" ]; then
        CLIENT_CMD="$CLIENT_CMD -max-duration \"$MAX_DURATION\""
    fi
    if [ -n "$MAX_SIZE" ]; then
        CLIENT_CMD="$CLIENT_CMD -max-size \"$MAX_SIZE\""
    fi
    
    # Wrap with namespace execution
    # Note: stdin redirection needs to be handled carefully with sudo
    CLIENT_CMD="sudo ip netns exec client bash -c \"cat \\\"$(pwd)/$OFFER_FILE\\\" | $CLIENT_CMD\""
    eval $CLIENT_CMD
else
    # Read offer from file and pass to client via stdin
    cat "$OFFER_FILE" | eval $CLIENT_CMD
fi

