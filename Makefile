.PHONY: usage tests

.DEFAULT: usage

usage:
	@echo "Usage:"
	@echo "    make tests           # Run unit tests"

tests:
	go test -v ./...

