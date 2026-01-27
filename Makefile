# Makefile for WebRTC Video Streaming Project
# 用于管理 Linux 平台的编译任务

# 目录配置
SRC_DIR := src
BUILD_DIR := build

# 源文件
CLIENT_SRC := $(SRC_DIR)/client.go $(SRC_DIR)/common.go
SERVER_SRC := $(SRC_DIR)/server.go $(SRC_DIR)/common.go

# 编译输出
CLIENT_BIN := $(BUILD_DIR)/client
SERVER_BIN := $(BUILD_DIR)/server

# Go 工具配置
GO := go
GOFLAGS := -v

# 默认目标：编译所有二进制文件
.PHONY: all
all: $(CLIENT_BIN) $(SERVER_BIN)
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

# 编译客户端二进制文件
$(CLIENT_BIN): $(CLIENT_SRC) | $(BUILD_DIR)
	@echo "Building client..."
	$(GO) build $(GOFLAGS) -o $(CLIENT_BIN) $(CLIENT_SRC)

# 编译服务器二进制文件
$(SERVER_BIN): $(SERVER_SRC) | $(BUILD_DIR)
	@echo "Building server..."
	$(GO) build $(GOFLAGS) -o $(SERVER_BIN) $(SERVER_SRC)

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

# 显示帮助信息
.PHONY: help
help:
	@echo "WebRTC Video Streaming Project - Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  make          - Build both client and server (default)"
	@echo "  make build    - Build both client and server"
	@echo "  make client   - Build client only"
	@echo "  make server   - Build server only"
	@echo "  make clean    - Remove build directory (keeps session_* directories)"
	@echo "  make fmt      - Format Go source code"
	@echo "  make vet      - Run go vet on source code"
	@echo "  make test     - Run tests"
	@echo "  make help     - Show this help message"
	@echo ""
	@echo "Source directory: $(SRC_DIR)"
	@echo "Build directory:  $(BUILD_DIR)"

