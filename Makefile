BINARY := lazycvs
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build release clean demo demo-setup demo-clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

release: clean
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe .

# Build the binary, generate a self-contained CVS demo working copy (history +
# real merge conflict), and launch lazycvs against it. Use this to exercise
# every TUI feature without needing a CVS server.
# Demo dir is prefixed with `_` so `go build ./...` and other Go tooling
# skip it (Go ignores _-prefixed directories during recursive walks).
DEMO_DIR := _lazycvs-demo

demo: build
	./scripts/demo.py ./$(DEMO_DIR)
	@echo
	@echo "==> launching lazycvs in $(CURDIR)/$(DEMO_DIR)/wc"
	@echo
	@cd $(DEMO_DIR)/wc && $(CURDIR)/$(BINARY)

# Setup only — generate the demo working copy without launching lazycvs.
# Useful if you want to inspect the demo state with cvs/your editor first.
demo-setup: build
	./scripts/demo.py ./$(DEMO_DIR)

demo-clean:
	rm -rf $(DEMO_DIR)

clean:
	rm -rf $(BINARY) dist/
