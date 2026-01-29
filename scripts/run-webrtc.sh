#!/bin/bash
# WebRTC video transmission script
# Usage: ./run-webrtc.sh [video_file] [server_ip] [client_ip]
#   video_file: Path to video file (default: Ultra.mp4)
#   server_ip:  IP address for server (optional, e.g., 192.168.100.1)
#   client_ip: IP address for client (optional, e.g., 192.168.100.2)
#              If only server_ip is provided, client will use auto-detect

set -e

# Parse arguments
DEFAULT_VIDEO="Ultra.mp4"
VIDEO_FILE="${1:-$DEFAULT_VIDEO}"
SERVER_IP="${2:-}"
CLIENT_IP="${3:-}"

# If server IP is specified but client IP is not, try to find a matching IP
# or use localhost for same-machine testing
if [ -n "$SERVER_IP" ] && [ -z "$CLIENT_IP" ]; then
    # Extract network prefix (e.g., 192.168.100 from 192.168.100.1)
    SERVER_NET=$(echo "$SERVER_IP" | cut -d'.' -f1-3)
    
    # Try to find a local IP in the same network
    LOCAL_IP=$(ip addr show | grep -E "inet.*$SERVER_NET" | head -1 | awk '{print $2}' | cut -d'/' -f1)
    
    if [ -n "$LOCAL_IP" ]; then
        CLIENT_IP="$LOCAL_IP"
        echo "Auto-detected client IP in same network: $CLIENT_IP"
    else
        # If no matching IP found, use localhost for same-machine testing
        # But first check if server IP is actually localhost
        if [ "$SERVER_IP" = "127.0.0.1" ] || [ "$SERVER_IP" = "localhost" ]; then
            CLIENT_IP="127.0.0.1"
        else
            # For different networks, we need to use the actual local IP
            # Get the first non-loopback IP
            CLIENT_IP=$(ip addr show | grep -E "inet " | grep -v "127.0.0.1" | head -1 | awk '{print $2}' | cut -d'/' -f1)
            if [ -z "$CLIENT_IP" ]; then
                CLIENT_IP="127.0.0.1"
            fi
            echo "Warning: Server IP $SERVER_IP not in local network. Using client IP: $CLIENT_IP"
        fi
    fi
fi

# Check if video file exists
if [ ! -f "$VIDEO_FILE" ]; then
    echo "Error: Video file not found: $VIDEO_FILE"
    echo "Usage: $0 [video_file] [server_ip] [client_ip]"
    echo "  Example: $0 Ultra.mp4 192.168.100.1 192.168.100.2"
    echo "  Example: $0 Ultra.mp4  (use localhost/auto-detect)"
    exit 1
fi

# Get absolute path
VIDEO_FILE=$(realpath "$VIDEO_FILE")

# Check if binaries exist
if [ ! -f "./server" ] || [ ! -f "./client" ]; then
    echo "Error: server or client binary not found. Please build them first:"
    echo "  go build -o server server.go"
    echo "  go build -o client client.go"
    exit 1
fi

# Create temporary files for SDP exchange
OFFER_FILE=$(mktemp)
ANSWER_FILE=$(mktemp)
SERVER_LOG=$(mktemp)
CLIENT_LOG=$(mktemp)
ANSWER_INPUT=$(mktemp)

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    exec 3>&- 2>/dev/null || true  # Close pipe if open
    kill $SERVER_PID $CLIENT_PID 2>/dev/null || true
    pkill -9 server 2>/dev/null || true
    pkill -9 client 2>/dev/null || true
    rm -f "$OFFER_FILE" "$ANSWER_FILE" "$SERVER_LOG" "$CLIENT_LOG" "$ANSWER_INPUT"
}

trap cleanup EXIT INT TERM

echo "=========================================="
echo "  WebRTC Video Transmission Test"
echo "=========================================="
echo "Video file: $VIDEO_FILE"
if [ -n "$SERVER_IP" ]; then
    echo "Server IP: $SERVER_IP"
    if [ -n "$CLIENT_IP" ]; then
        echo "Client IP: $CLIENT_IP"
    else
        echo "Client IP: auto-detect"
    fi
    echo "Running on LAN (IP addresses specified)"
else
    echo "Running on localhost (auto-detect IP)"
fi
echo ""

# Step 1: Start server in background
# Server will output offer to stdout, then read answer from stdin
echo "[1/4] Starting server..."
# Create empty files first to ensure they exist
touch "$OFFER_FILE" "$SERVER_LOG"
# Use unbuffered output (stdbuf disables buffering)
# Start server with /dev/null as stdin to avoid blocking on readUntilNewline
# Server will generate offer first, then we'll restart it with answer
if [ -n "$SERVER_IP" ]; then
    stdbuf -oL -eL ./server -video "$VIDEO_FILE" -ip "$SERVER_IP" < /dev/null > "$OFFER_FILE" 2>"$SERVER_LOG" &
else
    stdbuf -oL -eL ./server -video "$VIDEO_FILE" < /dev/null > "$OFFER_FILE" 2>"$SERVER_LOG" &
fi
SERVER_PID=$!
# Give server a moment to start
sleep 0.5

# Wait for offer to be generated
echo "  Waiting for offer (this may take a few seconds for ICE gathering)..."
OFFER_LINE=""
for i in {1..60}; do
    sleep 0.3
    # Check if file exists and has content
    if [ -f "$OFFER_FILE" ]; then
        FILE_SIZE=$(wc -c < "$OFFER_FILE" 2>/dev/null || echo "0")
        if [ "$FILE_SIZE" -gt 100 ]; then
            # Read the first line and extract base64
            RAW_LINE=$(head -1 "$OFFER_FILE" 2>/dev/null)
            if [ -n "$RAW_LINE" ]; then
                OFFER_LINE=$(echo "$RAW_LINE" | tr -d '\n\r\t ' | grep -oE '[A-Za-z0-9+/=]+' | head -1)
                if [ -n "$OFFER_LINE" ] && [ ${#OFFER_LINE} -gt 100 ]; then
                    echo "  ✓ Offer generated (${#OFFER_LINE} chars)"
                    break
                fi
            fi
        fi
    fi
    # Check if server died
    if ! kill -0 $SERVER_PID 2>/dev/null; then
        echo "Error: Server process died"
        echo "Server log:"
        cat "$SERVER_LOG" 2>/dev/null || echo "(no log)"
        exit 1
    fi
done

if [ -z "$OFFER_LINE" ] || [ ${#OFFER_LINE} -le 100 ]; then
    echo "Error: Server failed to generate offer within 18 seconds"
    echo "Server log:"
    cat "$SERVER_LOG" 2>/dev/null || echo "(no log)"
    echo ""
    if [ -f "$OFFER_FILE" ]; then
        FILE_SIZE=$(wc -c < "$OFFER_FILE" 2>/dev/null || echo "0")
        echo "Offer file exists, size: $FILE_SIZE bytes"
        if [ "$FILE_SIZE" -gt 0 ]; then
            echo "Offer file content (first 500 chars):"
            head -1 "$OFFER_FILE" | cut -c1-500
        fi
    else
        echo "Offer file does not exist"
    fi
    exit 1
fi

# Step 2: Start client to generate answer
echo "[2/4] Generating answer from client..."
if [ -n "$CLIENT_IP" ]; then
    echo "$OFFER_LINE" | ./client -ip "$CLIENT_IP" > "$ANSWER_FILE" 2>"$CLIENT_LOG" &
else
    echo "$OFFER_LINE" | ./client > "$ANSWER_FILE" 2>"$CLIENT_LOG" &
fi
CLIENT_PID=$!

# Wait for answer to be generated
echo "  Waiting for answer..."
for i in {1..30}; do
    sleep 1
    if [ -s "$ANSWER_FILE" ]; then
        if grep -q "^[A-Za-z0-9+/=]*$" "$ANSWER_FILE" 2>/dev/null; then
            ANSWER_LINE=$(grep "^[A-Za-z0-9+/=]*$" "$ANSWER_FILE" | head -1)
            if [ ${#ANSWER_LINE} -gt 100 ]; then
                echo "  ✓ Answer generated"
                break
            fi
        fi
    fi
    if ! kill -0 $CLIENT_PID 2>/dev/null; then
        echo "Error: Client process died"
        cat "$CLIENT_LOG"
        exit 1
    fi
    if [ $i -eq 30 ]; then
        echo "Error: Client failed to generate answer within 30 seconds"
        cat "$CLIENT_LOG"
        exit 1
    fi
done

# Extract the answer line
ANSWER_LINE=$(grep "^[A-Za-z0-9+/=]*$" "$ANSWER_FILE" | head -1)
if [ -z "$ANSWER_LINE" ]; then
    echo "Error: Could not extract answer from client output"
    cat "$ANSWER_FILE"
    cat "$CLIENT_LOG"
    exit 1
fi

# Step 3: Send answer to server
echo "[3/4] Sending answer to server..."
# Kill the first server instance (it was just for generating offer)
kill $SERVER_PID 2>/dev/null || true
timeout 2 bash -c "while kill -0 $SERVER_PID 2>/dev/null; do sleep 0.1; done" 2>/dev/null || true

# Write answer to temporary file
printf "%s\n" "$ANSWER_LINE" > "$ANSWER_INPUT"

# Restart server with answer as input
echo "  Restarting server with answer..."
if [ -n "$SERVER_IP" ]; then
    stdbuf -oL -eL ./server -video "$VIDEO_FILE" -ip "$SERVER_IP" < "$ANSWER_INPUT" > /dev/null 2>>"$SERVER_LOG" &
else
    stdbuf -oL -eL ./server -video "$VIDEO_FILE" < "$ANSWER_INPUT" > /dev/null 2>>"$SERVER_LOG" &
fi
SERVER_PID=$!
sleep 1
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Error: Server failed to start"
    echo "Server log:"
    tail -20 "$SERVER_LOG"
    exit 1
fi

# Wait a bit for connection to establish
sleep 2

# Check if server is still running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Error: Server died after restart"
    echo "Server log:"
    tail -30 "$SERVER_LOG"
    exit 1
fi

echo "[4/4] Connection established, streaming video..."
echo ""
echo "=========================================="
echo "  Streaming in progress..."
echo "  Server PID: $SERVER_PID"
echo "  Client PID: $CLIENT_PID"
echo "  Press Ctrl+C to stop"
echo "=========================================="
echo ""

# Monitor connection state (show logs)
tail -f "$SERVER_LOG" "$CLIENT_LOG" 2>/dev/null &
TAIL_PID=$!

# Wait for user interrupt or process completion
# Use a loop to check both processes
while kill -0 $SERVER_PID 2>/dev/null || kill -0 $CLIENT_PID 2>/dev/null; do
    sleep 1
done

kill $TAIL_PID 2>/dev/null || true

echo ""
echo "Streaming completed."
