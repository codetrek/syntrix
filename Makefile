.PHONY: build run test clean

CLI_APP_NAME=syntrix-cli
APP_NAME=syntrix
PULLER_APP_NAME=puller
BUILD_DIR=bin

ifeq ($(OS),Windows_NT)
	MKDIR_P = powershell -NoProfile -Command "if (-not (Test-Path '$(BUILD_DIR)')) { New-Item -ItemType Directory -Path '$(BUILD_DIR)' | Out-Null }"
	RM_RF = powershell -NoProfile -Command "if (Test-Path '$(BUILD_DIR)') { Remove-Item -Recurse -Force '$(BUILD_DIR)' }"
	EXE_EXT = .exe
else
    MKDIR_P = mkdir -p $(BUILD_DIR)
    RM_RF = rm -rf $(BUILD_DIR)
	EXE_EXT =
endif

APP_BIN=$(BUILD_DIR)/$(APP_NAME)$(EXE_EXT)
CLI_BIN=$(BUILD_DIR)/$(CLI_APP_NAME)$(EXE_EXT)

build:
	@$(MKDIR_P)
	@echo "Building $(CLI_APP_NAME)..."
	@go build -o $(CLI_BIN) ./cmd/syntrix-cli
	@echo "Building $(APP_NAME)..."
	@go build -o $(APP_BIN) ./cmd/syntrix
	@echo "Building $(PULLER_APP_NAME)..."
	@go build -o $(BUILD_DIR)/$(PULLER_APP_NAME)$(EXE_EXT) ./cmd/puller

run: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --all

run-query: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --query

run-csp: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --csp

run-api: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --api

run-trigger-evaluator: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --trigger-evaluator

run-trigger-worker: build
	@echo "Running $(APP_NAME)..."
	@$(APP_BIN) --trigger-worker

run-cli: build
	@echo "Running $(CLI_APP_NAME)..."
	@$(CLI_BIN)

test:
	@echo Running tests...
	@go test ./... -count=1

ifeq ($(OS),Windows_NT)
    COVERAGE_CMD = scripts\coverage.cmd detail
else
    COVERAGE_CMD = ./scripts/coverage.sh detail
endif

coverage:
	@echo Running tests with coverage...
	@$(COVERAGE_CMD)

clean:
	@echo "Cleaning..."
	@$(RM_RF)
