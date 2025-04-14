BINARY_NAME := handle-on-tag
PKG := ./cmd/on-tag
OUT_DIR := bin

.PHONY: all build run clean

all: build

build:
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(OUT_DIR)
	go build -o $(OUT_DIR)/$(BINARY_NAME) $(PKG)

run: build
	@echo "🚀 Running $(BINARY_NAME)..."
	./$(OUT_DIR)/$(BINARY_NAME)

run-token: build
	@echo "🚀 Running with token..."
	@echo '{"repo":"TRAINFACED/a","tag":"v0.0.44"}' | ./$(OUT_DIR)/$(BINARY_NAME) --token $$METADATA_REPO_PAT

clean:
	@echo "🧹 Cleaning up..."
	@rm -rf $(OUT_DIR)