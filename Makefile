.PHONY: build clean install service

BINARY_CLI=bubbles
BINARY_DAEMON=bubblesd
BUILD_DIR=./bin
export PATH := /usr/local/go/bin:$(PATH)

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/$(BINARY_CLI) ./cmd/bubbles
	CGO_ENABLED=1 go build -o $(BUILD_DIR)/$(BINARY_DAEMON) ./cmd/bubblesd
	@echo "✓ Built $(BUILD_DIR)/$(BINARY_CLI) and $(BUILD_DIR)/$(BINARY_DAEMON)"

install: build
	@pgrep -x '$(BINARY_DAEMON)' > /dev/null 2>&1 && { echo "⏹ Stopping $(BINARY_DAEMON)..."; pkill -x '$(BINARY_DAEMON)'; sleep 1; } || true
	cp $(BUILD_DIR)/$(BINARY_CLI) /usr/local/bin/
	cp $(BUILD_DIR)/$(BINARY_DAEMON) /usr/local/bin/
	@echo "✓ Installed to /usr/local/bin/"
	@if systemctl is-enabled bubblesd.service >/dev/null 2>&1; then \
		echo "▶ Restarting $(BINARY_DAEMON) via systemd..."; \
		systemctl restart bubblesd.service; \
		sleep 0.5; \
		systemctl is-active bubblesd.service >/dev/null && echo "✓ bubblesd.service active" || echo "✗ bubblesd.service failed to start"; \
	else \
		echo "▶ Starting $(BINARY_DAEMON) (nohup; 崩溃不会自动拉起，建议 make service 托管到 systemd)..."; \
		mkdir -p $(HOME)/.bubbles; \
		nohup $(BINARY_DAEMON) >> $(HOME)/.bubbles/bubblesd.log 2>&1 & \
		sleep 0.5; \
		pgrep -x '$(BINARY_DAEMON)' > /dev/null 2>&1 && echo "✓ $(BINARY_DAEMON) started (PID: $$(pgrep -x '$(BINARY_DAEMON)'))" || echo "✗ Failed to start $(BINARY_DAEMON)"; \
	fi

# 把 bubblesd 交给 systemd 托管：崩溃/被杀/OOM 自动拉起，停止时清理其派生子进程
service: install
	@echo "▶ Installing systemd unit bubblesd.service..."
	sed 's|@HOME@|$(HOME)|g' deploy/systemd/bubblesd.service > /etc/systemd/system/bubblesd.service
	chmod 644 /etc/systemd/system/bubblesd.service
	systemctl daemon-reload
	@pkill -x '$(BINARY_DAEMON)' >/dev/null 2>&1 && sleep 1 || true
	systemctl enable --now bubblesd.service
	@sleep 0.5
	@systemctl is-active bubblesd.service >/dev/null \
		&& echo "✓ bubblesd.service active (日志: journalctl -u bubblesd / ~/.bubbles/bubblesd.log)" \
		|| { echo "✗ bubblesd.service failed to start"; systemctl status bubblesd.service --no-pager -l | tail -20; exit 1; }

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
