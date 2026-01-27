# ==============================================================================
# Nimbus 函数计算平台 Makefile
# ==============================================================================
# 此文件定义了项目的构建、测试、部署等常用命令
# 使用方法: make [目标名]

# 声明所有伪目标（不对应实际文件的目标）
.PHONY: all build build-linux build-cli clean test test-coverage lint fmt mod-tidy run build-rootfs api-docs help web-install web-dev web-build web-clean k8s-up k8s-down k8s-logs

# ==============================================================================
# 变量定义
# ==============================================================================

# 二进制文件输出目录
BINARY_DIR := bin

# Go 编译器
GO := go

# 当前构建平台（linux/darwin/windows...）
HOST_GOOS := $(shell $(GO) env GOOS)

# 版本信息（从 git 获取）
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# 链接器标志：将版本信息编译进二进制文件
LDFLAGS := -ldflags "-X github.com/oriys/nimbus/cmd/nimbus/cmd.Version=$(VERSION) \
	-X github.com/oriys/nimbus/cmd/nimbus/cmd.GitCommit=$(GIT_COMMIT) \
	-X github.com/oriys/nimbus/cmd/nimbus/cmd.BuildDate=$(BUILD_DATE)"

# ==============================================================================
# 构建目标
# ==============================================================================

# 默认目标：构建所有组件
all: build

# build: 为当前平台构建所有二进制文件
# 这是开发时最常用的构建命令
build:
	@echo "Building gateway..."
	$(GO) build -o $(BINARY_DIR)/gateway ./cmd/gateway
ifeq ($(HOST_GOOS),linux)
	@echo "Building scheduler..."
	$(GO) build -o $(BINARY_DIR)/scheduler ./cmd/scheduler
	@echo "Building vmpool..."
	$(GO) build -o $(BINARY_DIR)/vmpool ./cmd/vmpool
	@echo "Building agent..."
	$(GO) build -o $(BINARY_DIR)/agent ./cmd/agent
else
	@echo "Skipping linux-only binaries (scheduler/vmpool/agent) on $(HOST_GOOS)"
endif
	@echo "Building CLI..."
	$(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus ./cmd/nimbus
	@echo "Building MCP server..."
	$(GO) build -o $(BINARY_DIR)/mcp-server ./cmd/mcp-server
	@echo "Build complete"

# build-linux: 交叉编译 Linux amd64 版本
# 用于在 macOS/Windows 上构建 Linux 部署包
build-linux:
	@echo "Building for Linux..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_DIR)/gateway-linux ./cmd/gateway
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_DIR)/scheduler-linux ./cmd/scheduler
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_DIR)/vmpool-linux ./cmd/vmpool
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_DIR)/agent-linux ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-linux ./cmd/nimbus
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_DIR)/mcp-server-linux ./cmd/mcp-server

# build-cli: 为所有平台构建 CLI 工具
# 生成 macOS (Intel/Apple Silicon)、Linux、Windows 版本
build-cli:
	@echo "Building CLI for multiple platforms..."
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-darwin-amd64 ./cmd/nimbus
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-darwin-arm64 ./cmd/nimbus
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-linux-amd64 ./cmd/nimbus
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-linux-arm64 ./cmd/nimbus
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BINARY_DIR)/nimbus-windows-amd64.exe ./cmd/nimbus
	@echo "CLI binaries built in $(BINARY_DIR)/"

# install-cli: 将 CLI 工具安装到系统路径
# 安装后可以直接使用 nimbus 命令
install-cli: build
	@echo "Installing nimbus to /usr/local/bin..."
	sudo cp $(BINARY_DIR)/nimbus /usr/local/bin/nimbus
	@echo "Installed. Run 'nimbus --help' to get started."

# install-dev: 在本地 bin 目录创建符号链接，方便开发
install-dev: build
	@echo "Creating symlink for nimbus in /usr/local/bin..."
	sudo ln -sf $(PWD)/$(BINARY_DIR)/nimbus /usr/local/bin/nimbus
	@echo "Symlink created. Run 'nimbus --help' to test."

# shell-completion: 生成并安装 shell 补全脚本
shell-completion: build
	@echo "Generating shell completion scripts..."
	@mkdir -p $(BINARY_DIR)/completion
	$(BINARY_DIR)/nimbus completion bash > $(BINARY_DIR)/completion/nimbus.bash
	$(BINARY_DIR)/nimbus completion zsh > $(BINARY_DIR)/completion/nimbus.zsh
	@echo "Completion scripts generated in $(BINARY_DIR)/completion/"
	@echo "To enable zsh completion, add 'source <(nimbus completion zsh)' to your .zshrc"
	@echo "To enable bash completion, add 'source <(nimbus completion bash)' to your .bashrc"

# clean: 清理构建产物
# 删除二进制文件和临时文件
clean:
	rm -rf $(BINARY_DIR)
	rm -rf /opt/firecracker/sockets/*
	rm -rf /opt/firecracker/logs/*

# ==============================================================================
# 测试目标
# ==============================================================================

# test: 运行所有单元测试
test:
	$(GO) test -v ./...

# test-coverage: 运行测试并生成覆盖率报告
# 生成 coverage.html 可在浏览器中查看
test-coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# lint: 运行代码检查
# 需要先安装 golangci-lint
lint:
	golangci-lint run ./...

# fmt: 格式化代码
# 使用 Go 标准格式化工具
fmt:
	$(GO) fmt ./...

# mod-tidy: 整理 Go 模块依赖
# 移除未使用的依赖，添加缺失的依赖
mod-tidy:
	$(GO) mod tidy

# ==============================================================================
# 开发运行目标
# ==============================================================================

# run: 构建并运行网关服务
# 用于本地开发测试
run: build
	./$(BINARY_DIR)/gateway -config configs/config.yaml

# ==============================================================================
# Kubernetes (OrbStack) 目标
# ==============================================================================

# k8s-up: 启动 OrbStack Kubernetes 环境
# 一键启动完整的开发环境
k8s-up:
	@echo "Starting OrbStack Kubernetes environment..."
	cd deployments/k8s/overlays/orbstack && ./start.sh

# k8s-down: 停止 OrbStack Kubernetes 环境
# 保留数据
k8s-down:
	@echo "Stopping OrbStack Kubernetes environment..."
	cd deployments/k8s/overlays/orbstack && ./stop.sh

# k8s-logs: 查看 Gateway 日志
k8s-logs:
	kubectl logs -f -n nimbus -l app=nimbus-gateway -c gateway

# ==============================================================================
# Web 控制台目标
# ==============================================================================

# web-install: 安装前端依赖
# 首次构建前需要运行此命令
web-install:
	@echo "Installing web dependencies..."
	cd web && npm install

# web-dev: 启动前端开发服务器
# 支持热重载，用于开发调试
web-dev:
	@echo "Starting web development server..."
	cd web && npm run dev

# web-build: 构建前端生产版本
# 输出到 web/dist 目录
web-build:
	@echo "Building web frontend..."
	cd web && npm run build

# web-clean: 清理前端构建产物
web-clean:
	@echo "Cleaning web build artifacts..."
	rm -rf web/dist web/node_modules/.cache

# web-lint: 检查前端代码规范
web-lint:
	cd web && npm run lint

# ==============================================================================
# 其他目标
# ==============================================================================

# build-rootfs: 构建 Firecracker 根文件系统
# 需要 root 权限执行
build-rootfs:
	sudo ./scripts/build-rootfs.sh

# api-docs: 生成 API 文档
# 使用 swag 工具从代码注释生成 Swagger 文档
api-docs:
	swag init -g cmd/gateway/main.go -o docs

# help: 显示帮助信息
# 列出所有可用的 make 目标
help:
	@echo "Nimbus - Firecracker Serverless Platform"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build targets:"
	@echo "  build        Build all binaries for current platform"
	@echo "  build-linux  Build all binaries for Linux amd64"
	@echo "  build-cli    Build CLI for multiple platforms"
	@echo "  clean        Remove build artifacts"
	@echo ""
	@echo "Test targets:"
	@echo "  test          Run all tests"
	@echo "  test-coverage Run tests with coverage report"
	@echo "  lint          Run linter"
	@echo ""
	@echo "Development targets:"
	@echo "  run           Build and run gateway"
	@echo "  fmt           Format code"
	@echo "  mod-tidy      Tidy go modules"
	@echo ""
	@echo "Kubernetes (OrbStack) targets:"
	@echo "  k8s-up        Start OrbStack Kubernetes environment"
	@echo "  k8s-down      Stop OrbStack Kubernetes environment"
	@echo "  k8s-logs      View Gateway logs"
	@echo ""
	@echo "Web console targets:"
	@echo "  web-install   Install web frontend dependencies"
	@echo "  web-dev       Start web development server (hot reload)"
	@echo "  web-build     Build web frontend for production"
	@echo "  web-clean     Clean web build artifacts"
	@echo "  web-lint      Run web frontend linter"
	@echo ""
	@echo "Other targets:"
	@echo "  install-cli   Install CLI to /usr/local/bin"
	@echo "  build-rootfs  Build rootfs images (requires root)"
	@echo "  api-docs      Generate API documentation"

# 设置默认目标为 all
.DEFAULT_GOAL := all
