#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")"

PATH="$HOME/go/bin:$PATH" protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-vtproto_out=. --go-vtproto_opt=paths=source_relative,features=marshal+unmarshal+unmarshal_unsafe+size \
  schema.proto
