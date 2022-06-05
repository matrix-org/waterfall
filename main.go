package main

import (
	"flag"
	"fmt"
	yaml "gopkg.in/yaml.v3"
	"io/ioutil"
	"log"

	"maunium.net/go/mautrix/id"
)

func loadConfig(configFilePath string) (*config, error) {
	log.Printf("Loading %s", configFilePath)
	file, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		log.Fatal("Failed to read config", err)
	}
	var config config
	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal YAML: %s", err)
	}
	return &config, nil
}

func main() {
	configFilePath := flag.String("config", "config.yaml", "Configuration file path")
	flag.Parse()

	var config *config
	var err error
	if config, err = loadConfig(*configFilePath); err != nil {
		log.Fatal("Failed to load config file", err)
	}

	if err := initMatrix(config); err != nil {
		log.Fatal("Failed to init Matrix", err)
	}
}

type config struct {
	UserID        id.UserID
	HomeserverURL string
	AccessToken   string
}

type trackDesc struct {
	StreamID string `json:"stream_id"`
	TrackID  string `json:"track_id"`
}

type dataChannelMessage struct {
	Op      string `json:"op"`
	ID      string `json:"id"`
	Message string `json:"message,omitempty`
	// XXX: is this even needed? we know which conf a given call is for...
	ConfID string      `json:"conf_id,omitempty"`
	Start  []trackDesc `json:"start,omitempty"`
	Stop   []trackDesc `json:"stop,omitempty"`
	SDP    string      `json:"sdp,omitempty"`
}
