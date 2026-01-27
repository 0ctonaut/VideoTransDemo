#!/bin/bash
# WebRTC Server startup script
# Usage: ./server.sh -video <video_file> [-ip <ip_address>]

set -e

# Parse arguments
VIDEO_FILE=""
SERVER_IP=""
LOOP_VIDEO=""
USE_NETNS=""
MM_DELAY=""
MM_LOSS=""
MM_LINK=""

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
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 -video <video_file> [-ip <ip_address>] [-loop] [-netns] [--mmdelay <delay>] [--mmloss <loss>] [--mmlink <link>] [--mmlink-default]"
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

# Check and auto-build server if needed
# 如果二进制文件不存在，或者源文件比二进制文件新，则自动编译
# 注意：脚本在 scripts/ 目录，需要回到项目根目录
cd "$(dirname "$0")/.." || exit 1
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
# Note: Don't use quotes in SERVER_CMD when it will be used in mahimahi chain
# mahimahi tools need the command as separate arguments
SERVER_CMD="./build/server -video $VIDEO_FILE -offer-file $OFFER_FILE -answer-file $ANSWER_FILE"

if [ -n "$SERVER_IP" ]; then
    SERVER_CMD="$SERVER_CMD -ip $SERVER_IP"
fi

if [ -n "$LOOP_VIDEO" ]; then
    SERVER_CMD="$SERVER_CMD -loop"
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
            # mm-link chains with mm-delay/mm-loss without -- separator
            MM_CMD="mm-link ${DEFAULT_TRACE}.up ${DEFAULT_TRACE}.down"
            echo "Using default traces: ${DEFAULT_TRACE}.{up,down}"
        else
            echo "Warning: Default TMobile-LTE-driving traces not found, skipping mm-link"
        fi
    fi
    
    # Add mm-delay if specified
    # Note: mm-link needs -- to separate its args from command, but mm-delay/mm-loss chain without --
    if [ -n "$MM_DELAY" ] && [ -n "$MM_CMD" ]; then
        MM_CMD="$MM_CMD mm-delay $MM_DELAY"
    elif [ -n "$MM_DELAY" ]; then
        MM_CMD="mm-delay $MM_DELAY"
    fi
    
    # Add mm-loss if specified (apply to both uplink and downlink)
    if [ -n "$MM_LOSS" ]; then
        if [ -n "$MM_CMD" ]; then
            # If MM_CMD starts with mm-link, we already have -- separator, so chain without --
            # If MM_CMD starts with mm-delay, chain without -- separator
            if [[ "$MM_CMD" == mm-link* ]]; then
                # mm-link already has --, so chain mm-loss without --
                MM_CMD="$MM_CMD mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
            else
                # mm-delay chain, no -- needed
                MM_CMD="$MM_CMD mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
            fi
        else
            MM_CMD="mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
        fi
    fi
    
    # Add the original command (SERVER_CMD already has no quotes)
    # mm-link needs -- to separate its options from command (when command starts with -)
    if [ -n "$MM_CMD" ]; then
        # Check if MM_CMD starts with mm-link (needs -- before command)
        if [[ "$MM_CMD" == mm-link* ]]; then
            MM_CMD="$MM_CMD -- $SERVER_CMD"
        else
            # mm-delay/mm-loss accept command directly
            MM_CMD="$MM_CMD $SERVER_CMD"
        fi
    fi
fi

# Wrap command with namespace if -netns is used
if [ -n "$USE_NETNS" ]; then
    # Use absolute path for binary and files to ensure they're accessible in namespace
    ABS_SERVER_BIN="$(pwd)/build/server"
    ABS_VIDEO_FILE="$(readlink -f "$VIDEO_FILE" 2>/dev/null || echo "$(pwd)/$VIDEO_FILE")"
    ABS_OFFER_FILE="$(pwd)/$OFFER_FILE"
    ABS_ANSWER_FILE="$(pwd)/$ANSWER_FILE"
    
    # Rebuild command with absolute paths (no quotes for mahimahi compatibility)
    SERVER_CMD="$ABS_SERVER_BIN -video $ABS_VIDEO_FILE -offer-file $ABS_OFFER_FILE -answer-file $ABS_ANSWER_FILE -ip $SERVER_IP"
    if [ -n "$LOOP_VIDEO" ]; then
        SERVER_CMD="$SERVER_CMD -loop"
    fi
    
    # If mahimahi is used, rebuild MM_CMD with updated SERVER_CMD
    if [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ] || [ -n "$MM_LINK" ]; then
        # Rebuild mahimahi chain with updated SERVER_CMD
        MM_CMD_REBUILT=""
        if [ -n "$MM_LINK" ]; then
            if [ "$MM_LINK" = "default" ]; then
                # User explicitly requested default traces
                DEFAULT_TRACE=$(find_default_traces)
                if [ -n "$DEFAULT_TRACE" ]; then
                    MM_CMD_REBUILT="mm-link ${DEFAULT_TRACE}.up ${DEFAULT_TRACE}.down"
                else
                    echo "Error: Default TMobile-LTE-driving traces not found"
                    exit 1
                fi
            else
                # User specified link (trace file path)
                LINK_PARTS=($MM_LINK)
                if [ ${#LINK_PARTS[@]} -eq 2 ]; then
                    UPLINK="${LINK_PARTS[0]}"
                    DOWNLINK="${LINK_PARTS[1]}"
                    MM_CMD_REBUILT="mm-link $UPLINK $DOWNLINK"
                else
                    MM_CMD_REBUILT="mm-link $MM_LINK $MM_LINK"
                fi
            fi
        elif [ -n "$MM_DELAY" ] || [ -n "$MM_LOSS" ]; then
            DEFAULT_TRACE=$(find_default_traces)
            if [ -n "$DEFAULT_TRACE" ]; then
                MM_CMD_REBUILT="mm-link ${DEFAULT_TRACE}.up ${DEFAULT_TRACE}.down"
            fi
        fi
        
        if [ -n "$MM_DELAY" ] && [ -n "$MM_CMD_REBUILT" ]; then
            MM_CMD_REBUILT="$MM_CMD_REBUILT mm-delay $MM_DELAY"
        elif [ -n "$MM_DELAY" ]; then
            MM_CMD_REBUILT="mm-delay $MM_DELAY"
        fi
        
        if [ -n "$MM_LOSS" ]; then
            if [ -n "$MM_CMD_REBUILT" ]; then
                MM_CMD_REBUILT="$MM_CMD_REBUILT mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
            else
                MM_CMD_REBUILT="mm-loss uplink $MM_LOSS mm-loss downlink $MM_LOSS"
            fi
        fi
        
        if [ -n "$MM_CMD_REBUILT" ]; then
            # Check if MM_CMD_REBUILT starts with mm-link (needs -- before command)
            if [[ "$MM_CMD_REBUILT" == mm-link* ]]; then
                MM_CMD_REBUILT="$MM_CMD_REBUILT -- $SERVER_CMD"
            else
                # mm-delay/mm-loss accept command directly
                MM_CMD_REBUILT="$MM_CMD_REBUILT $SERVER_CMD"
            fi
            SERVER_CMD="sudo ip netns exec server $MM_CMD_REBUILT"
        else
            SERVER_CMD="sudo ip netns exec server $SERVER_CMD"
        fi
    else
        SERVER_CMD="sudo ip netns exec server $SERVER_CMD"
    fi
elif [ -n "$MM_CMD" ]; then
    # Only mahimahi, no netns
    SERVER_CMD="$MM_CMD"
fi

# Run server
eval $SERVER_CMD

