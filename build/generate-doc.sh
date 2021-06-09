#!/bin/sh
# Generate resources go-file

set -e

esc -modtime="0" -prefix md -o internal/docfs/resources.go -pkg docfs md
