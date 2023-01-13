#!/usr/bin/env bash

go test -race -v ./... -bench=. -benchmem
