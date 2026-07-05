BINARY := pdm
PKG := ./cmd/pdm

.PHONY: build test vet fmt lint run data clean install

build:
	go build -o $(BINARY) $(PKG)

install:
	go install $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet
	gofmt -l .

# Regenerate the bundled snapshot from the official geoservices.
data:
	go run ./cmd/pdmdata

clean:
	rm -f $(BINARY)

# Quick smoke test of the pilot.
run: build
	./$(BINARY) point 39.60 -8.41
