#!/usr/bin/env bash

go run ./pkg/*.go --cpuProfile cpuProfile.pprof --memProfile memProfile.pprof --logTime
