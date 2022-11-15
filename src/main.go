/*
Copyright 2022 The Matrix.org Foundation C.I.C.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"

	"github.com/sirupsen/logrus"
)

func initCPUProfiling(cpuProfile *string) func() {
	logrus.Info("initializing CPU profiling")

	file, err := os.Create(*cpuProfile)
	if err != nil {
		logrus.WithError(err).Fatal("could not create CPU profile")
	}

	if err := pprof.StartCPUProfile(file); err != nil {
		logrus.WithError(err).Fatal("could not start CPU profile")
	}

	return func() {
		pprof.StopCPUProfile()

		if err := file.Close(); err != nil {
			logrus.WithError(err).Fatal("could not close CPU profile")
		}
	}
}

func initMemoryProfiling(memProfile *string) func() {
	logrus.Info("initializing memory profiling")

	return func() {
		file, err := os.Create(*memProfile)
		if err != nil {
			logrus.WithError(err).Fatal("could not create memory profile")
		}

		runtime.GC()

		if err := pprof.WriteHeapProfile(file); err != nil {
			logrus.WithError(err).Fatal("could not write memory profile")
		}

		if err = file.Close(); err != nil {
			logrus.WithError(err).Fatal("could not close memory profile")
		}
	}
}

func initLogging(logTime *bool) {
	formatter := new(CustomTextFormatter)

	formatter.logTime = *logTime

	logrus.SetFormatter(formatter)
}

func killListener(c chan os.Signal, beforeExit []func()) {
	<-c

	for _, function := range beforeExit {
		function()
	}

	defer os.Exit(0)
}

func main() {
	var (
		logTime        = flag.Bool("logTime", false, "whether or not to print time and date in logs")
		configFilePath = flag.String("config", "config.yaml", "configuration file path")
		cpuProfile     = flag.String("cpuProfile", "", "write CPU profile to `file`")
		memProfile     = flag.String("memProfile", "", "write memory profile to `file`")
	)

	flag.Parse()

	initLogging(logTime)

	beforeExit := []func(){}
	if *cpuProfile != "" {
		beforeExit = append(beforeExit, initCPUProfiling(cpuProfile))
	}

	if *memProfile != "" {
		beforeExit = append(beforeExit, initMemoryProfiling(memProfile))
	}

	// try to handle os interrupt(signal terminated)
	//nolint:gomnd
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go killListener(c, beforeExit)

	config, err := loadConfig(*configFilePath)
	if err != nil {
		logrus.WithError(err).Fatal("could not load config")
	}

	InitMatrix(config)
}
