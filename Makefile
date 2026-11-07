# The Snello commands, each run inside the image build/Dockerfile
# describes unless NATIVE=1 is given, so that a green run on a laptop
# and a green job in CI mean the same thing: make build, the compiler
# into bin/snello, the one Go command here since the compiler is
# written in Go; make test, snello test over the library's and the
# examples' packages through both backends, which make test-std and
# make test-examples run apart; make check, snello check over the
# examples; make shell, the container with the repository mounted.
# The Go suite and the benchmarks are go test's, run by CI through
# build/run.sh.

IMAGE := snello-dev
GO_VERSION := $(shell awk '/^go /{print $$2}' go.mod)
RUN := build/run.sh

ifdef NATIVE
NEEDS :=
else
NEEDS := image
endif

.PHONY: image build test test-std test-examples check shell clean

image:
	docker build --build-arg GO_VERSION=$(GO_VERSION) -t $(IMAGE) build

build: $(NEEDS)
	$(RUN) go build -o bin/snello ./cmd/snello

test: test-std test-examples

test-std: build
	$(RUN) sh build/test-std.sh

test-examples: build
	$(RUN) sh build/test-examples.sh

check: build
	$(RUN) sh build/check.sh

shell: image
	docker run --rm -it --user "$$(id -u):$$(id -g)" -v "$(CURDIR)":/src -w /src -e HOME=/tmp -e CC=clang -e CLANG=clang $(IMAGE) bash

clean:
	rm -rf bin
