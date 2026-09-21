#!/bin/zsh
set -e
cd "$(dirname "$0")"
make build
exec ./bin/ntty
