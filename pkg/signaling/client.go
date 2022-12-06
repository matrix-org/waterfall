package signaling

import (
	"github.com/sirupsen/logrus"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
)

type MatrixClient struct {
	client *mautrix.Client
}

func NewMatrixClient(config Config) *MatrixClient {
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

	return &MatrixClient{
		client: client,
	}
}

// Starts the Matrix client and connects to the homeserver,
// Returns only when the sync with Matrix fails.
func (m *MatrixClient) RunSyncing(callback func(*event.Event)) {
	syncer, ok := m.client.Syncer.(*mautrix.DefaultSyncer)
	if !ok {
		logrus.Panic("Syncer is not DefaultSyncer")
	}

	syncer.ParseEventContent = true
	syncer.OnEvent(func(_ mautrix.EventSource, evt *event.Event) {
		// We only care about to-device events but also receive m.presence and
		// m.push_rules events; we can simply ignore those.
		if evt.Type.Class != event.ToDeviceEventType {
			return
		}

		// We drop the messages if they are not meant for us.
		if evt.Content.Raw["dest_session_id"] != LocalSessionID {
			logrus.Warn("SessionID does not match our SessionID - ignoring")
			return
		}

		callback(evt)
	})

	// TODO: We may want to reconnect if `Sync()` fails instead of ending the SFU
	//       as ending here will essentially drop all conferences which may not necessarily
	// 	     be what we want for the existing running conferences.
	if err := m.client.Sync(); err != nil {
		logrus.WithError(err).Panic("Sync failed")
	}
}
