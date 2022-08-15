#!/usr/bin/env bash

go run ./src/*.go --cpuProfile cpuProfile.pprof --memProfile memProfile.pprof --logTime
