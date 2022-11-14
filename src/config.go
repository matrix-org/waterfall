package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix/id"
)

// The mandatory SFU configuration.
type Config struct {
	// The Matrix ID (MXID) of the SFU.
	UserID id.UserID
	// The ULR of the homeserver that SFU talks to.
	HomeserverURL string
	// The access token for the Matrix SDK.
	AccessToken string
	// Keep-alive timeout for WebRTC connections. If no keep-alive has been received
	// from the client for this duration, the connection is considered dead.
	KeepAliveTimeout int
}

// Load config from the provided string.
// Returns an error if the string is not a valid YAML.
func loadConfigFromString(configString string) (*Config, error) {
	logrus.Info("loading config from string")

	var config Config
	if err := yaml.Unmarshal([]byte(configString), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML file: %w", err)
	}

	return &config, nil
}

// ErrNoConfigEnvVar is returned when the CONFIG environment variable is not set.
var ErrNoConfigEnvVar = errors.New("environment variable not set or invalid")

// Tries to load the config from environment variables.
// Returns an error if not all environment variables are set.
func loadConfigFromEnv() (*Config, error) {
	configEnv := os.Getenv("CONFIG")
	if configEnv == "" {
		return nil, ErrNoConfigEnvVar
	}

	return loadConfigFromString(configEnv)
}

// Tries to load a config from the provided path.
func loadConfigFromPath(path string) (*Config, error) {
	logrus.WithField("path", path).Info("loading config")

	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return loadConfigFromString(string(file))
}
