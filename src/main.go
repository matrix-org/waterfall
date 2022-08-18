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
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"

	yaml "gopkg.in/yaml.v3"
	"maunium.net/go/mautrix/id"
)

type Config struct {
	UserID        id.UserID
	HomeserverURL string
	AccessToken   string
	Timeout       int
}

var config *Config

var logTime = flag.Bool("logTime", false, "whether or not to print time and date in logs")
var configFilePath = flag.String("config", "config.yaml", "configuration file path")
var cpuProfile = flag.String("cpuProfile", "", "write CPU profile to `file`")
var memProfile = flag.String("memProfile", "", "write memory profile to `file`")

func initCPUProfiling(cpuProfile *string) func() {
	log.Print("initializing CPU profiling")

	file, err := os.Create(*cpuProfile)
	if err != nil {
		log.Fatalf("could not create CPU profile: %s", err)
	}

	if err := pprof.StartCPUProfile(file); err != nil {
		log.Fatalf("could not start CPU profile: %s", err)
	}

	return func() {
		pprof.StopCPUProfile()

		if err := file.Close(); err != nil {
			log.Fatalf("could not close CPU profile: %s", err)
		}
	}
}

func initMemoryProfiling(memProfile *string) func() {
	log.Print("initializing memory profiling")

	return func() {
		file, err := os.Create(*memProfile)
		if err != nil {
			log.Fatalf("could not create memory profile: %s", err)
		}

		runtime.GC()

		if err := pprof.WriteHeapProfile(file); err != nil {
			log.Fatalf("could not write memory profile: %s", err)
		}

		if err = file.Close(); err != nil {
			log.Fatalf("could not close memory profile: %s", err)
		}
	}
}

func initLogging(logTime *bool) {
	log.SetFlags(0)

	if *logTime {
		log.SetFlags(log.Ldate | log.Ltime)
	}
}

func loadConfig(configFilePath string) (*Config, error) {
	log.Printf("loading %s", configFilePath)

	file, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		log.Fatalf("failed to read config: %s", err)
	}
	var config Config

	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &config, nil
}

func killListener(c chan os.Signal, beforeExit []func()) {
	<-c
	log.Printf("ending program")

	for _, function := range beforeExit {
		function()
	}

	defer os.Exit(0)
}

func main() {
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

	var err error
	if config, err = loadConfig(*configFilePath); err != nil {
		log.Fatalf("failed to load config file: %s", err)
	}

	InitMatrix()
}
