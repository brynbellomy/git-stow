.PHONY: build install vet fmt-check test check

build:
	go build -o bin/git-stow ./cmd/git-stow

install:
	go install ./cmd/git-stow

vet:
	go vet ./...

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# -count=1: the e2e tests build git-stow themselves, which go test's cache cannot see.
test:
	go test -count=1 ./...

check: fmt-check vet test
