.PHONY: build run test clean

CLI_APP_NAME=syntrix-cli
APP_NAME=syntrix
BUILD_DIR=bin

build:
	@mkdir -p $(BUILD_DIR)
	@echo "Building $(CLI_APP_NAME)..."
	@go build -o $(BUILD_DIR)/$(CLI_APP_NAME) ./cmd/syntrix-cli
	@echo "Building $(APP_NAME)..."
	@go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/syntrix

run: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME) --all

run-realtime: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME) --realtime

run-query: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME) --query

run-csp: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME) --csp

run-api: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME) --api

run-cli: build
	@echo "Running $(CLI_APP_NAME)..."
	@./$(BUILD_DIR)/$(CLI_APP_NAME)

test:
	@echo "Running tests..."
	@go test ./... -count=1

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
