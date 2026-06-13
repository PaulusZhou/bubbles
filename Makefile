.PHONY: build clean install

BINARY_CLI=bubbles
BINARY_DAEMON=bubblesd
BUILD_DIR=./bin

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/$(BINARY_CLI) ./cmd/bubbles
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/$(BINARY_DAEMON) ./cmd/bubblesd
	@echo "✓ Built $(BUILD_DIR)/$(BINARY_CLI) and $(BUILD_DIR)/$(BINARY_DAEMON)"

install: build
	cp $(BUILD_DIR)/$(BINARY_CLI) /usr/local/bin/
	cp $(BUILD_DIR)/$(BINARY_DAEMON) /usr/local/bin/
	@echo "✓ Installed to /usr/local/bin/"

clean:
	rm -rf $(BUILD_DIR)
	rm -f $(HOME)/.bubbles/bubblesd.sock $(HOME)/.bubbles/bubblesd.pid
	@echo "✓ Cleaned"

tidy:
	go mod tidy

test:
	go test ./...

run: build
	$(BUILD_DIR)/$(BINARY_DAEMON)
