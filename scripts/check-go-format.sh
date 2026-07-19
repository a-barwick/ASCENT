#!/bin/sh
set -eu

files=$(gofmt -l apps internal protocol/cmd protocol/gen)
if [ -n "$files" ]; then
  echo "Go files require formatting:"
  echo "$files"
  echo "Run: make format"
  exit 1
fi
