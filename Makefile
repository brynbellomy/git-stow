.PHONY: build install vet fmt-check test check

build:
	go build -o bin/git-stow ./cmd/git-stow

install:
	go install ./cmd/git-stow

vet:
	go vet ./...

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

test:
	go test ./...

check: fmt-check vet test
