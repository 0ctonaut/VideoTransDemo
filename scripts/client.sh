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
MM_DELAY=""
MM_LOSS=""
MM_LINK=""

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
        --mmdelay)
            MM_DELAY="$2"
            shift 2
            ;;
        --mmloss)
            MM_LOSS="$2"
            shift 2
            ;;
        --mmlink)
            MM_LINK="$2"
            shift 2
            ;;
        --mmlink-default|--mmlink-default)
            MM_LINK="default"
            shift
            ;;
        -*)
            echo "Unknown option: $1"
            echo "Usage: $0 [session_folder] [-ip <ip_address>] [-output <output_file>] [-max-duration <duration>] [-max-size <MB>] [-netns] [--mmdelay <delay>] [--mmloss <loss>] [--mmlink <link>] [--mmlink-default]"
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

# Find default TMobile-LTE-driving trace files
find_default_traces() {
    TRACE_DIRS=(
        "/usr/local/share/mahimahi/traces"
        "/usr/share/mahimahi/traces"
        "$HOME/mahimahi/traces"
    )
    
    for dir in "${TRACE_DIRS[@]}"; do
        if [ -f "$dir/TMobile-LTE-driving.up" ] && [ -f "$dir/TMobile-LTE-driving.down" ]; then
            echo "$dir/TMobile-LTE-driving"
            return 0
        fi
    done
    return 1
}

# Check mahimahi tools if mahimahi parameters are specified
if [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ] || [ -n "$MM_LINK" ]; then
    if ! command -v mm-link &> /dev/null || ! command -v mm-delay &> /dev/null || ! command -v mm-loss &> /dev/null; then
        echo "Error: mahimahi tools not found. Please install mahimahi or ensure they are in PATH."
        exit 1
    fi
fi

# Check and auto-build client if needed
# 如果二进制文件不存在，或者源文件比二进制文件新，则自动编译
# 注意：脚本在 scripts/ 目录，需要回到项目根目录
cd "$(dirname "$0")/.." || exit 1
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
if [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ] || [ -n "$MM_LINK" ]; then
    echo "Mahimahi network emulation: enabled"
    if [ -n "$MM_DELAY" ]; then
        echo "  - Delay: ${MM_DELAY}ms"
    fi
    if [ -n "$MM_LOSS" ]; then
        echo "  - Loss: ${MM_LOSS} ($(echo "$MM_LOSS * 100" | bc -l | xargs printf "%.2f")%)"
    fi
    if [ -n "$MM_LINK" ]; then
        echo "  - Link: $MM_LINK"
    else
        echo "  - Link: TMobile-LTE-driving (default)"
    fi
    if [ -n "$USE_NETNS" ]; then
        echo "  - Note: Running mahimahi inside network namespace (nested mode)"
    fi
fi
echo ""

# Build client command
# Note: Don't use quotes in CLIENT_CMD when it will be used in mahimahi chain
# mahimahi tools need the command as separate arguments
CLIENT_CMD="./build/client -answer-file $ANSWER_FILE -output $OUTPUT_FILE"

if [ -n "$CLIENT_IP" ]; then
    CLIENT_CMD="$CLIENT_CMD -ip $CLIENT_IP"
fi

if [ -n "$MAX_DURATION" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-duration $MAX_DURATION"
fi

if [ -n "$MAX_SIZE" ]; then
    CLIENT_CMD="$CLIENT_CMD -max-size $MAX_SIZE"
fi

# Build mahimahi command chain if mahimahi parameters are specified
MM_CMD=""
if [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ] || [ -n "$MM_LINK" ]; then
    # Determine uplink and downlink for mm-link
    if [ -n "$MM_LINK" ]; then
        # Check if user wants default traces
        if [ "$MM_LINK" = "default" ]; then
            # User explicitly requested default traces
            DEFAULT_TRACE=$(find_default_traces)
            if [ -n "$DEFAULT_TRACE" ]; then
                MM_CMD="mm-link ${DEFAULT_TRACE}.up ${DEFAULT_TRACE}.down"
                echo "Using default traces: ${DEFAULT_TRACE}.{up,down}"
            else
                echo "Error: Default TMobile-LTE-driving traces not found"
                exit 1
            fi
        else
            # User specified link (trace file path)
            # Check if it's two arguments (uplink downlink) or one (same for both)
            LINK_PARTS=($MM_LINK)
            if [ ${#LINK_PARTS[@]} -eq 2 ]; then
                # Two arguments: separate uplink and downlink
                UPLINK="${LINK_PARTS[0]}"
                DOWNLINK="${LINK_PARTS[1]}"
                MM_CMD="mm-link $UPLINK $DOWNLINK"
            else
                # One argument: use for both uplink and downlink
                MM_CMD="mm-link $MM_LINK $MM_LINK"
            fi
        fi
    elif [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ]; then
        # User specified delay or loss but not link, use default traces
        DEFAULT_TRACE=$(find_default_traces)
        if [ -n "$DEFAULT_TRACE" ]; then
            MM_CMD="mm-link ${DEFAULT_TRACE}.up ${DEFAULT_TRACE}.down"
            echo "Using default traces: ${DEFAULT_TRACE}.{up,down}"
        else
            echo "Warning: Default TMobile-LTE-driving traces not found, skipping mm-link"
        fi
    fi
    
    # Add mm-delay if specified
    # Note: mm-link chains with mm-delay/mm-loss without -- separator
    if [ -n "$MM_DELAY" ] && [ -n "$MM_CMD" ]; then
        # MM_CMD already contains mm-link, chain mm-delay directly (no --)
        MM_CMD="$MM_CMD mm-delay $MM_DELAY"
    elif [ -n "$MM_DELAY" ]; then
        MM_CMD="mm-delay $MM_DELAY"
    fi
    
    # Add mm-loss if specified (apply to both uplink and downlink)
    if [ -n "$MM_LOSS" ]; then
        if [ -n "$MM_CMD" ]; then
            # Chain mm-loss directly (no -- separator)
            MM_CMD="$MM_CMD mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
        else
            MM_CMD="mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
        fi
    fi
    
    # Note: We'll add the original command later, after handling stdin
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
    if [ -n "$MM_CMD" ]; then
        # Nested mode: mahimahi inside netns
        # Build the full command: mahimahi chain + client command
        # mm-link needs -- before command
        if [[ "$MM_CMD" == mm-link* ]]; then
            FULL_CMD="$MM_CMD -- $CLIENT_CMD"
        else
            FULL_CMD="$MM_CMD $CLIENT_CMD"
        fi
        CLIENT_CMD="sudo ip netns exec client bash -c \"cat \\\"$(pwd)/$OFFER_FILE\\\" | $FULL_CMD\""
    else
        CLIENT_CMD="sudo ip netns exec client bash -c \"cat \\\"$(pwd)/$OFFER_FILE\\\" | $CLIENT_CMD\""
    fi
    eval $CLIENT_CMD
else
    # Read offer from file and pass to client via stdin
    if [ -n "$MM_CMD" ]; then
        # Use mahimahi, need to handle stdin properly
        # Build the full command: mahimahi chain + client command
        # mm-link needs -- before command
        if [[ "$MM_CMD" == mm-link* ]]; then
            FULL_CMD="$MM_CMD -- $CLIENT_CMD"
        else
            FULL_CMD="$MM_CMD $CLIENT_CMD"
        fi
        cat "$OFFER_FILE" | eval $FULL_CMD
    else
        cat "$OFFER_FILE" | eval $CLIENT_CMD
    fi
fi

