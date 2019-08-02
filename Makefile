.PHONY: usage tests

.DEFAULT: usage

export GO111MODULE=on

usage:
	@echo "Usage:"
	@echo "    make tests           # Run unit tests"

tests:
	go test -v ./...

