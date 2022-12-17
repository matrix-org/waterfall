package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/matrix-org/waterfall/pkg/conference"
	"github.com/matrix-org/waterfall/pkg/signaling"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// SFU configuration.
type Config struct {
	// Matrix configuration.
	Matrix signaling.Config `yaml:"matrix"`
	// Conference (call) configuration.
	Conference conference.Config `yaml:"conference"`
	// Starting from which level to log stuff.
	LogLevel string `yaml:"log"`
}

// Tries to load a config from the `CONFIG` environment variable.
// If the environment variable is not set, tries to load a config from the
// provided path to the config file (YAML). Returns an error if the config could
// not be loaded.
func LoadConfig(path string) (*Config, error) {
	config, err := LoadConfigFromEnv()
	if err != nil {
		if !errors.Is(err, ErrNoConfigEnvVar) {
			return nil, err
		}

		return LoadConfigFromPath(path)
	}

	return config, nil
}

// ErrNoConfigEnvVar is returned when the CONFIG environment variable is not set.
var ErrNoConfigEnvVar = errors.New("environment variable not set or invalid")

// Tries to load the config from environment variable (`CONFIG`).
// Returns an error if not all environment variables are set.
func LoadConfigFromEnv() (*Config, error) {
	configEnv := os.Getenv("CONFIG")
	if configEnv == "" {
		return nil, ErrNoConfigEnvVar
	}

	return LoadConfigFromString(configEnv)
}

// Tries to load a config from the provided path.
func LoadConfigFromPath(path string) (*Config, error) {
	logrus.WithField("path", path).Info("loading config")

	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return LoadConfigFromString(string(file))
}

// Load config from the provided string.
// Returns an error if the string is not a valid YAML.
func LoadConfigFromString(configString string) (*Config, error) {
	logrus.Info("loading config from string")

	var config Config
	if err := yaml.Unmarshal([]byte(configString), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML file: %w", err)
	}

	if err := validateConfig(config); err != nil {
		return nil, err
	}

	return &config, nil
}

func validateConfig(config Config) error {
	if config.Matrix.UserID == "" {
		return fmt.Errorf("you must set matrix.userId")
	}
	if config.Matrix.HomeserverURL == "" {
		return fmt.Errorf("you must set matrix.homeserverUrl")
	}
	if config.Matrix.AccessToken == "" {
		return fmt.Errorf("you must set matrix.accessToken")
	}
	if config.Conference.HeartbeatConfig.Timeout == 0 {
		return fmt.Errorf("you must set heartbeat.timeout")
	}
	if config.Conference.HeartbeatConfig.Interval == 0 {
		return fmt.Errorf("you must set heartbeat.interval")
	}

	// Make sure the heartbeat values are within sane bounds
	if config.Conference.HeartbeatConfig.Timeout < 30 && config.Conference.HeartbeatConfig.Timeout > 60*2 {
		return fmt.Errorf("heartbeat.timeout must be between 30s and 2m")
	}
	if config.Conference.HeartbeatConfig.Interval < 5 && config.Conference.HeartbeatConfig.Interval > 30 {
		return fmt.Errorf("heartbeat.interval must be between 5s and 30s")
	}

	return nil
}
