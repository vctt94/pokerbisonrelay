#!/bin/sh

BINDIR=$(mktemp -d)

build_protoc_gen_go() {
    mkdir -p $BINDIR
    export GOBIN=$BINDIR
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
}

generate() {
    protoc --go_out=. --go-grpc_out=. poker.proto
}

# Build the bins from the main module, so that clientrpc doesn't need to
# require all client and tool dependencies.
(cd .. && build_protoc_gen_go)
GENPATH="$BINDIR:$PATH"
PATH=$GENPATH generate
