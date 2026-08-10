.PHONY: build test check run image install-systemd

build:
	go build -trimpath -o acebridge ./cmd/acebridge

test:
	go test -race ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -race ./...

run:
	go run ./cmd/acebridge

image:
	docker build -t acebridge:local .

install-systemd:
	./scripts/install-systemd.sh
