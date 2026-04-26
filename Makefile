VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w

.PHONY: build test release clean

build:
	go build -ldflags "$(LDFLAGS)" -o claude-mobile ./cmd/claude-mobile

test:
	go test ./...

release: clean
	@mkdir -p dist
	@for arch in amd64 arm64; do \
		echo "→ darwin-$$arch"; \
		mkdir -p dist/claude-mobile-darwin-$$arch; \
		GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" \
			-o dist/claude-mobile-darwin-$$arch/claude-mobile \
			./cmd/claude-mobile || exit 1; \
		cp LICENSE README.md dist/claude-mobile-darwin-$$arch/; \
		tar -C dist -czf dist/claude-mobile-darwin-$$arch.tar.gz claude-mobile-darwin-$$arch/; \
		rm -rf dist/claude-mobile-darwin-$$arch; \
	done
	@echo
	@ls -lh dist/*.tar.gz

clean:
	rm -rf dist/ claude-mobile
