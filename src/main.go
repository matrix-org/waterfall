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
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
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

// / Tries to load the config from environment variables.
// / Returns an error if not all environment variables are set.
func loadConfigFromEnv() (*Config, error) {
	userID := strings.TrimSpace(os.Getenv("USER_ID"))
	if userID == "" {
		return nil, fmt.Errorf("USER_ID environment variable not set")
	}

	homeserverURL := strings.TrimSpace(os.Getenv("HOMESERVER_URL"))
	if homeserverURL == "" {
		return nil, fmt.Errorf("HOMESERVER_URL environment variable not set")
	}

	accessToken := strings.TrimSpace(os.Getenv("ACCESS_TOKEN"))
	if accessToken == "" {
		return nil, fmt.Errorf("ACCESS_TOKEN environment variable not set")
	}

	timeoutStr := strings.TrimSpace(os.Getenv("TIMEOUT"))
	if timeoutStr == "" {
		return nil, fmt.Errorf("TIMEOUT environment variable not set")
	}

	timeout, err := strconv.Atoi(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("TIMEOUT environment variable is not a number")
	}

	return &Config{
		UserID:        id.UserID(userID),
		HomeserverURL: homeserverURL,
		AccessToken:   accessToken,
		Timeout:       timeout,
	}, nil
}

func loadConfig(configFilePath string) (*Config, error) {
	logrus.WithField("path", configFilePath).Info("loading config")

	file, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	var config Config

	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML file: %w", err)
	}

	return &config, nil
}

func killListener(c chan os.Signal, beforeExit []func()) {
	<-c

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
	config, err = loadConfigFromEnv()
	if err != nil {
		logrus.WithError(err).Info("failed to load config from environment variables, trying to load from file")

		if config, err = loadConfig(*configFilePath); err != nil {
			logrus.WithError(err).Fatal("failed to load config file")
		}
	}

	InitMatrix()
}
