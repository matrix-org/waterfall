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

	_ "net/http/pprof"
)

var configInstance *config

var configFilePath = flag.String("config", "config.yaml", "Configuration file path")
var cpuProfile = flag.String("cpuProfile", "", "write CPU profile to `file`")
var memProfile = flag.String("memProfile", "", "write memory profile to `file`")

func initCpuProfiling(cpuProfile *string) func() {
	log.Print("initializing CPU profiling")

	f, err := os.Create(*cpuProfile)
	if err != nil {
		log.Fatalf("could not create CPU profile: %s", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		log.Fatalf("could not start CPU profile: %s", err)
	}

	return func() {
		pprof.StopCPUProfile()
		if err := f.Close(); err != nil {
			log.Fatalf("could not close CPU profile: %s", err)
		}
	}
}

func initMemoryProfiling(memProfile *string) func() {
	log.Print("initializing memory profiling")

	return func() {
		f, err := os.Create(*memProfile)
		if err != nil {
			log.Fatalf("could not create memory profile: %s", err)
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatalf("could not write memory profile: %s", err)
		}
		if err = f.Close(); err != nil {
			log.Fatalf("could not close memory profile: %s", err)
		}
	}
}

func initLogging() {
	log.SetFlags(log.Ldate | log.Ltime)
}

func loadConfig(configFilePath string) (*config, error) {
	log.Printf("loading %s", configFilePath)
	file, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		log.Fatalf("failed to read config: %s", err)
	}
	var config config
	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %s", err)
	}
	return &config, nil
}

func onKill(c chan os.Signal, beforeExit []func()) {
	select {
	case <-c:
		log.Printf("ending program")

		for _, function := range beforeExit {
			function()
		}
		defer os.Exit(0)
	}
}

func main() {
	initLogging()

	flag.Parse()

	beforeExit := []func(){}
	if *cpuProfile != "" {
		beforeExit = append(beforeExit, initCpuProfiling(cpuProfile))
	}
	if *memProfile != "" {
		beforeExit = append(beforeExit, initMemoryProfiling(memProfile))
	}

	// try to handle os interrupt(signal terminated)
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go onKill(c, beforeExit)

	var err error
	if configInstance, err = loadConfig(*configFilePath); err != nil {
		log.Fatalf("failed to load config file: %s", err)
	}

	if err := initMatrix(); err != nil {
		log.Fatalf("failed to init Matrix: %s", err)
	}
}

type config struct {
	UserID        id.UserID
	HomeserverURL string
	AccessToken   string
	Timeout       int
}

type trackDesc struct {
	StreamID string `json:"stream_id"`
	TrackID  string `json:"track_id"`
}

type dataChannelMessage struct {
	Op      string `json:"op"`
	ID      string `json:"id"`
	Message string `json:"message,omitempty"`
	// XXX: is this even needed? we know which conf a given call is for...
	ConfID string      `json:"conf_id,omitempty"`
	Start  []trackDesc `json:"start,omitempty"`
	Stop   []trackDesc `json:"stop,omitempty"`
	SDP    string      `json:"sdp,omitempty"`
}
