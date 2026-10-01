#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
  exit 0
fi

unformatted=$(gofmt -l "$@") || exit "$?"
if [ -n "$unformatted" ]; then
  printf '%s\n' "$unformatted"
  exit 1
fi
