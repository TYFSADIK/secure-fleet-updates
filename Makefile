.PHONY: build test
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o bin/fleet-ota-arm64 ./cmd/fleet-ota
	go build -o bin/fleet-ota ./cmd/fleet-ota
test:
	go vet ./... && go test -race -v ./... 2>&1 | grep -E "^(--- |ok|FAIL)"
