#!/usr/bin/env bash

set -e
echo "" > coverage.txt

for d in $(go list ./... | grep -v vendor); do
    echo -e "TESTS FOR: for \033[0;35m${d}\033[0m"
    go test -v $d
done
