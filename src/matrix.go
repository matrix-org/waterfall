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
	"fmt"

	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
)

const localSessionID = "sfu"

func RunSFU(config *Config) {
	client, err := mautrix.NewClient(config.HomeserverURL, config.UserID, config.AccessToken)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client")
	}

	whoami, err := client.Whoami()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to identify SFU user")
	}

	if config.UserID != whoami.UserID {
		logrus.WithField("user_id", config.UserID).Fatal("Access token is for the wrong user")
	}

	logrus.WithField("device_id", whoami.DeviceID).Info("Identified SFU as DeviceID")
	client.DeviceID = whoami.DeviceID

	focus := NewFocus(
		fmt.Sprintf("%s (%s)", config.UserID, client.DeviceID),
		client,
		&ConferenceConfig{KeepAliveTimeout: config.KeepAliveTimeout},
	)

	syncer, ok := client.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		logrus.Panic("Syncer is not DefaultSyncer")
	}

	syncer.ParseEventContent = true

	// TODO: E2EE
	syncer.OnEvent(focus.onEvent)

	if err = client.Sync(); err != nil {
		logrus.WithError(err).Panic("Sync failed")
	}
}
