# Makefile for WebRTC Video Streaming Project
# 用于管理 Linux 平台的编译任务

# 目录配置
SRC_DIR := src
BUILD_DIR := build

# 源文件
CLIENT_SRC := $(SRC_DIR)/client.go $(SRC_DIR)/common.go $(SRC_DIR)/h264_writer.go
SERVER_SRC := $(SRC_DIR)/server.go $(SRC_DIR)/common.go

# GCC 客户端/服务器源文件（GCC 实验）
CLIENT_GCC_SRC := $(SRC_DIR)/client-gcc.go $(SRC_DIR)/common.go $(SRC_DIR)/metrics.go $(SRC_DIR)/h264_writer.go
SERVER_GCC_SRC := $(SRC_DIR)/server-gcc.go $(SRC_DIR)/common.go $(SRC_DIR)/server_ffmpeg_gcc.go

# NDTC 源文件
SERVER_NDTC_SRC := $(SRC_DIR)/server_ndtc.go $(SRC_DIR)/common.go $(SRC_DIR)/ndtc_controller.go $(SRC_DIR)/fdace_estimator.go $(SRC_DIR)/server_ffmpeg_ndtc.go
CLIENT_NDTC_SRC := $(SRC_DIR)/client_ndtc.go $(SRC_DIR)/common.go $(SRC_DIR)/metrics.go $(SRC_DIR)/h264_writer.go

# Salsify 源文件
SERVER_SALSIFY_SRC := $(SRC_DIR)/server_salsify.go $(SRC_DIR)/common.go $(SRC_DIR)/salsify_controller.go $(SRC_DIR)/server_ffmpeg_salsify.go
CLIENT_SALSIFY_SRC := $(SRC_DIR)/client_salsify.go $(SRC_DIR)/common.go $(SRC_DIR)/metrics.go $(SRC_DIR)/salsify_controller.go $(SRC_DIR)/h264_writer.go

# BurstRTC 源文件
SERVER_BURST_SRC := $(SRC_DIR)/server_burst.go $(SRC_DIR)/common.go $(SRC_DIR)/burst_controller.go $(SRC_DIR)/server_ffmpeg_burst.go
CLIENT_BURST_SRC := $(SRC_DIR)/client_burst.go $(SRC_DIR)/common.go $(SRC_DIR)/metrics.go $(SRC_DIR)/burst_controller.go $(SRC_DIR)/h264_writer.go

# 编译输出
CLIENT_BIN := $(BUILD_DIR)/client
SERVER_BIN := $(BUILD_DIR)/server
CLIENT_GCC_BIN := $(BUILD_DIR)/client-gcc
SERVER_GCC_BIN := $(BUILD_DIR)/server-gcc
SERVER_NDTC_BIN := $(BUILD_DIR)/server-ndtc
CLIENT_NDTC_BIN := $(BUILD_DIR)/client-ndtc
SERVER_SALSIFY_BIN := $(BUILD_DIR)/server-salsify
CLIENT_SALSIFY_BIN := $(BUILD_DIR)/client-salsify
SERVER_BURST_BIN := $(BUILD_DIR)/server-burst
CLIENT_BURST_BIN := $(BUILD_DIR)/client-burst

# Go 工具配置
GO := go
GOFLAGS := -v

# 默认目标：编译核心二进制文件（基础 client/server + GCC 实验）
.PHONY: all
all: $(CLIENT_BIN) $(SERVER_BIN) $(CLIENT_GCC_BIN) $(SERVER_GCC_BIN)
	@echo "Build completed successfully!"

# build 是 all 的别名
.PHONY: build
build: all

# 编译客户端
.PHONY: client
client: $(CLIENT_BIN)
	@echo "Client built successfully!"

# 编译服务器
.PHONY: server
server: $(SERVER_BIN)
	@echo "Server built successfully!"

# 编译 GCC 客户端
.PHONY: client-gcc
client-gcc: $(CLIENT_GCC_BIN)
	@echo "GCC client built successfully!"

# 编译 GCC 服务器
.PHONY: server-gcc
server-gcc: $(SERVER_GCC_BIN)
	@echo "GCC server built successfully!"

# 编译客户端二进制文件
$(CLIENT_BIN): $(CLIENT_SRC) | $(BUILD_DIR)
	@echo "Building client..."
	$(GO) build $(GOFLAGS) -o $(CLIENT_BIN) $(CLIENT_SRC)

# 编译服务器二进制文件
$(SERVER_BIN): $(SERVER_SRC) | $(BUILD_DIR)
	@echo "Building server..."
	$(GO) build $(GOFLAGS) -o $(SERVER_BIN) $(SERVER_SRC)

# 编译 GCC 客户端
$(CLIENT_GCC_BIN): $(CLIENT_GCC_SRC) | $(BUILD_DIR)
	@echo "Building GCC client..."
	$(GO) build $(GOFLAGS) -tags gcc -o $(CLIENT_GCC_BIN) $(CLIENT_GCC_SRC)

# 编译 GCC 服务器
$(SERVER_GCC_BIN): $(SERVER_GCC_SRC) | $(BUILD_DIR)
	@echo "Building GCC server..."
	$(GO) build $(GOFLAGS) -tags gcc -o $(SERVER_GCC_BIN) $(SERVER_GCC_SRC)

# 编译 NDTC 服务器
.PHONY: server-ndtc
server-ndtc: $(SERVER_NDTC_BIN)
	@echo "NDTC server built successfully!"

$(SERVER_NDTC_BIN): $(SERVER_NDTC_SRC) | $(BUILD_DIR)
	@echo "Building NDTC server..."
	$(GO) build $(GOFLAGS) -tags ndtc -o $(SERVER_NDTC_BIN) $(SERVER_NDTC_SRC)

# 编译 NDTC 客户端
.PHONY: client-ndtc
client-ndtc: $(CLIENT_NDTC_BIN)
	@echo "NDTC client built successfully!"

$(CLIENT_NDTC_BIN): $(CLIENT_NDTC_SRC) | $(BUILD_DIR)
	@echo "Building NDTC client..."
	$(GO) build $(GOFLAGS) -tags ndtc -o $(CLIENT_NDTC_BIN) $(CLIENT_NDTC_SRC)

# 编译 Salsify 服务器
.PHONY: server-salsify
server-salsify: $(SERVER_SALSIFY_BIN)
	@echo "Salsify server built successfully!"

$(SERVER_SALSIFY_BIN): $(SERVER_SALSIFY_SRC) | $(BUILD_DIR)
	@echo "Building Salsify server..."
	$(GO) build $(GOFLAGS) -tags salsify -o $(SERVER_SALSIFY_BIN) $(SERVER_SALSIFY_SRC)

# 编译 Salsify 客户端
.PHONY: client-salsify
client-salsify: $(CLIENT_SALSIFY_BIN)
	@echo "Salsify client built successfully!"

$(CLIENT_SALSIFY_BIN): $(CLIENT_SALSIFY_SRC) | $(BUILD_DIR)
	@echo "Building Salsify client..."
	$(GO) build $(GOFLAGS) -tags salsify -o $(CLIENT_SALSIFY_BIN) $(CLIENT_SALSIFY_SRC)

# 编译 BurstRTC 服务器
.PHONY: server-burst
server-burst: $(SERVER_BURST_BIN)
	@echo "BurstRTC server built successfully!"

$(SERVER_BURST_BIN): $(SERVER_BURST_SRC) | $(BUILD_DIR)
	@echo "Building BurstRTC server..."
	$(GO) build $(GOFLAGS) -tags burst -o $(SERVER_BURST_BIN) $(SERVER_BURST_SRC)

# 编译 BurstRTC 客户端
.PHONY: client-burst
client-burst: $(CLIENT_BURST_BIN)
	@echo "BurstRTC client built successfully!"

$(CLIENT_BURST_BIN): $(CLIENT_BURST_SRC) | $(BUILD_DIR)
	@echo "Building BurstRTC client..."
	$(GO) build $(GOFLAGS) -tags burst -o $(CLIENT_BURST_BIN) $(CLIENT_BURST_SRC)

# 创建 build 目录（如果不存在）
$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)
	@echo "Created $(BUILD_DIR) directory"

# 清理编译输出（不清理 session）
.PHONY: clean
clean:
	@echo "Cleaning build directory..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean completed!"

# 格式化代码
.PHONY: fmt
fmt:
	@echo "Formatting Go code..."
	$(GO) fmt ./$(SRC_DIR)/...
	@echo "Formatting completed!"

# 代码检查
.PHONY: vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./$(SRC_DIR)/...
	@echo "Vet completed!"

# 运行测试（如果有）
.PHONY: test
test:
	@echo "Running tests..."
	$(GO) test ./$(SRC_DIR)/...
	@echo "Tests completed!"

# 一键编译所有算法
.PHONY: all-algorithms
all-algorithms: $(CLIENT_GCC_BIN) $(SERVER_GCC_BIN) $(CLIENT_NDTC_BIN) $(SERVER_NDTC_BIN) $(CLIENT_SALSIFY_BIN) $(SERVER_SALSIFY_BIN) $(CLIENT_BURST_BIN) $(SERVER_BURST_BIN)
	@echo "All algorithms built successfully!"

# 显示帮助信息
.PHONY: help
help:
	@echo "WebRTC Video Streaming Project - Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make                - Build both client and server (default, basic version)"
	@echo "  make build          - Build both client and server"
	@echo "  make client         - Build client only"
	@echo "  make server         - Build server only"
	@echo ""
	@echo "Algorithm-specific targets:"
	@echo "  make client-gcc     - Build GCC client"
	@echo "  make server-gcc     - Build GCC server"
	@echo "  make client-ndtc    - Build NDTC client"
	@echo "  make server-ndtc    - Build NDTC server"
	@echo "  make client-salsify - Build Salsify client"
	@echo "  make server-salsify - Build Salsify server"
	@echo "  make client-burst   - Build BurstRTC client"
	@echo "  make server-burst   - Build BurstRTC server"
	@echo "  make all-algorithms - Build all algorithms (GCC, NDTC, Salsify, BurstRTC)"
	@echo ""
	@echo "Other targets:"
	@echo "  make clean    - Remove build directory (keeps session_* directories)"
	@echo "  make fmt      - Format Go source code"
	@echo "  make vet      - Run go vet on source code"
	@echo "  make test     - Run tests"
	@echo "  make help     - Show this help message"
	@echo ""
	@echo "Source directory: $(SRC_DIR)"
	@echo "Build directory:  $(BUILD_DIR)"

