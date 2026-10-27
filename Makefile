VERSION ?= dev
GOFLAGS ?=
LDFLAGS = -s -w -X suse-checkin/internal/version.Version=$(VERSION)

.PHONY: build run test release clean

build:
	CGO_ENABLED=0 go build -trimpath -buildvcs=false $(GOFLAGS) -ldflags "$(LDFLAGS)" -o suse-checkin ./cmd/server

run:
	go run ./cmd/server

test:
	gofmt -l .
	go vet ./...
	go test ./...

release:
	./scripts/build-release.sh "$(VERSION)"

clean:
	rm -rf dist suse-checkin
