#!/usr/bin/env bash

clear && go run ./src/*.go --cpuProfile cpuProfile.pprof --memProfile memProfile.pprof --logTime
