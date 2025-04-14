BINARY_NAME := handle-on-tag
PKG := ./cmd/on-tag
OUT_DIR := bin

.PHONY: all build run clean

all: build

build:
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(OUT_DIR)
	go build -o $(OUT_DIR)/$(BINARY_NAME) $(PKG)