#!/usr/bin/env bash

docker build --build-arg BUILD_VERSION=$(date +%Y%m%d-%H%M%S)-001 -t kedge .