#!/bin/sh
# Unit checks inside the Go toolchain container. The workstation has no Go.
set -eu
files=$(gofmt -l .)
if [ -n "$files" ]; then
	printf '%s\n' "$files"
	exit 1
fi
go vet ./...
# -race needs cgo. The dashboard binary is still built with CGO_ENABLED=0.
CGO_ENABLED=1 go test -race -count=1 ./...
