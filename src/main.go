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
	"syscall"

	"github.com/matrix-org/waterfall/src/config"
	"github.com/matrix-org/waterfall/src/signaling"
	"github.com/sirupsen/logrus"
)

func main() {
	// Parse command line flags.
	var (
		logTime        = flag.Bool("logTime", false, "whether or not to print time and date in logs")
		configFilePath = flag.String("config", "config.yaml", "configuration file path")
		cpuProfile     = flag.String("cpuProfile", "", "write CPU profile to `file`")
		memProfile     = flag.String("memProfile", "", "write memory profile to `file`")
	)
	flag.Parse()

	// Initialize logging subsystem (formatting, global logging framework etc).
	InitLogging(*logTime)

	// Define functions that are called before exiting.
	// This is useful to stop the profiler if it's enabled.
	deferred_functions := []func(){}
	if *cpuProfile != "" {
		deferred_functions = append(deferred_functions, InitCPUProfiling(cpuProfile))
	}
	if *memProfile != "" {
		deferred_functions = append(deferred_functions, InitMemoryProfiling(memProfile))
	}

	// Handle signal interruptions.
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		for _, function := range deferred_functions {
			function()
		}
		os.Exit(0)
	}()

	// Load the config file from the environment variable or path.
	config, err := config.LoadConfig(*configFilePath)
	if err != nil {
		logrus.WithError(err).Fatal("could not load config")
	}

	signaling.RunServer(config)
}
