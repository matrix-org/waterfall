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

	if err := ValidateConfig(config); err != nil {
		return nil, err
	}

	return &config, nil
}

func ValidateConfig(config Config) error {
	errored := false

	if config.Matrix.UserID == "" {
		errored = true
		fmt.Println("You must set matrix.userId")
	}
	if config.Matrix.HomeserverURL == "" {
		errored = true
		fmt.Println("You must set matrix.homeserverUrl")
	}
	if config.Matrix.AccessToken == "" {
		errored = true
		fmt.Println("You must set matrix.accessToken")
	}
	if config.Conference.PingPongConfig.Timeout == 0 {
		errored = true
		fmt.Println("You must set pingPong.timeout")
	}
	if config.Conference.PingPongConfig.Interval == 0 {
		errored = true
		fmt.Println("You must set pingPong.interval")
	}

	if config.Conference.PingPongConfig.Timeout > 30 {
		errored = true
		fmt.Println("pingPong.timeout must be 30s or lower")
	}
	if config.Conference.PingPongConfig.Interval < 30 {
		errored = true
		fmt.Println("pingPong.interval must be 30s or higher")
	}

	if config.Conference.PingPongConfig.Timeout < 5 {
		errored = true
		fmt.Println("It is not recommended for pingPong.timeout to be below 5s")
	}
	if config.Conference.PingPongConfig.Interval > 60*5 {
		errored = true
		fmt.Println("It is not recommended for pingPong.interval to be more than 300s")
	}

	if errored {
		return errors.New("invalid config values")
	} else {
		return nil
	}
}
