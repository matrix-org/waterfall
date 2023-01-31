# Local development environment

> A description how to set up and run _Element Call_ with the _SFU_ on a local machine.

Please note this all is still work in progress and will change in the future.

## What we need?

To run _Element Call_ local we need three components:

- A Matrix Home Server like [Synapse](https://matrix.org/docs/projects/server/synapse), [Dentrite](https://matrix.org/docs/projects/server/dendrite), ...
- The _SFU_ [Waterfall](https://github.com/matrix-org/waterfall) 
- _Element Call_ [Element Call](https://element.io/blog/introducing-native-matrix-voip-with-element-call/)

## Get it run!

### Start the Backend
As a backend we use _Synapse_ and docker to build them.

_Synapse_ takes over the user and the room management and the event handling for the connection establishment.
To do this, we later need to create users in _Synapse_ .
Important information is that _SFU_ also registers with an _SFU_ user account on the Homeserver _Synapse_.
But first we'll set up _Synapse_ and build a configuration for them. 

#### Create a _Synapse_ configuration
We'll set up _Synapse_ in a way that's it will store the database files as well as the configuration files in a local data directory.
Having it local makes it much easier to do changes on it.

Run the following commands in a directory of your choice!

Create a `data` directory.
```bash
mkdir data
```
As next let us create a default _Synapse_ configuration in `data` folder.
```shell
docker run -it --rm \
    -v $(pwd)/data:/data \
    -e SYNAPSE_SERVER_NAME=localhost \
    -e SYNAPSE_REPORT_STATS=no \
    matrixdotorg/synapse:latest generate
```

#### Customize the _Synapse_ configuration
Let us customize the `data/homeserver.yml` configuration a little.
This will make our live easier in local development process:

**Activate unsecure registration**: Its help us to register users easier:
For this add the following lines in the `data/homeserver.yml`
```yaml
enable_registration: true
enable_registration_without_verification: true
```

**Deactivate Reporting** by adding this:
```yaml
report_stats: false
```

**Increase the Rate Limit**: Because we need to do sometime load testing, it is a good thing that's _Synapse_ allows massive logins from the same IP.
For this add the following lines in the `data/homeserver.yml`
```yaml
rc_login:
  address:
    per_second:  15
    burst_count: 5
  account:
    per_second:  18
    burst_count: 4
  failed_attempts:
    per_second: 19
    burst_count: 7
```

#### Run the Homeserver

```shell
docker run -d --name shadowfax-synapse \
    -v $(pwd)/data:/data \
    -p 8008:8008 \
    matrixdotorg/synapse:latest
```

#### Create the SFU user and some other
We need to create the SFU user in _Synapse_.

```shell
docker exec -it shadowfax-synapse register_new_matrix_user http://localhost:8008 -c /data/homeserver.yaml -u sfu  -p sfu --no-admin
```
```shell
docker exec -it shadowfax-synapse register_new_matrix_user http://localhost:8008 -c /data/homeserver.yaml -u user1 -p user1 --no-admin
```
```shell
docker exec -it shadowfax-synapse register_new_matrix_user http://localhost:8008 -c /data/homeserver.yaml -u admin -p admin --admin
```

#### Run Admin Tool (optional)

Sometimes it is nice to see what happen in the Homeserver. 
For this you can use this tool and the admin user we created a step previous to login.

```shell
docker run -d -p 8081:80 awesometechnologies/synapse-admin
```

---

### Start the Frontend
_Element Call_ is the frontend component. 
The main part of the WebRTC logic you will find is in the [Matrix JS SDK](https://github.com/matrix-org/matrix-js-sdk).
_Element Call_ needs to know the _SFU_ in order to connect to it.
For this checkout the project with the current feature branch:

```bash
git clone https://github.com/vector-im/element-call.git
git checkout feature-sfu
```
Create a config file (`element-call/public/config.json`) in the public folder of the root of the project with the follow content:

```json
{
  "default_server_config": {
    "m.homeserver": {
      "base_url": "http://localhost:8008",
      "server_name": "localhost"
    }
  },
  "temp_sfu": {
    "user_id": "@sfu:localhost",
    "device_id": "<<SFU_DEVICE_ID>>"
  }
}
```

The `<<SFU_DEVICE_ID>>` is the device ID of the _SFU_ you not currently have.
We will create them now.

### Register the SFU user and get the `SFU_DEVICE_ID` and `ACCESS_TOKEN`
We use the frontend to register the _SFU_ user. 
That's a bit hacky but for now the easiest way.
Later we provide the device id via a script. 
But because every device ID also has device keys, we use the frontend to create both.
At the same time, we receive the access token in this way and do not have to create them manually.

For this, build and run the Frontend with the following commands in _Element Call_ project:

```
yarn install
yarn dev
```

Open a browser and navigate to http://localhost:3000
Open in the browser dev console the network tab.
Now log in with your _SFU_ user and check the request in the network tab.
You should find the login request response like this:
```
http://localhost:8008/_matrix/client/r0/login
```

```json
{
  "access_token": "<<ACCESS_TOKEN>>",
  "device_id": "<<SFU_DEVICE_ID>>",
  "home_server": "localhost",
  "user_id": "@sfu:localhost"
}
```

Take out the device id and the access token. 
The device keys created doing the login.
Don't forget to log out now.

Add the Device id in your `element-call/public/config.json` and restart the _Element Call_ app.

---

### Start the SFU

Check out the main branch of the _SFU_ and then first create a `config.yml` in the root directory of the _SFU_.

```
git clone https://github.com/matrix-org/waterfall.git
cd waterfall
cp config.yaml.sample config.yaml
```

Find the following lines of config and change thm with your <<ACCESS_TOKEN>> and your local settings.

```
matrix:
  homeserverUrl: "http://localhost:8008"
  userId: "@sfu:localhost"
  accessToken: "<<ACCESS_TOKEN>>"
  
...  
```

Start the _SFU_ in your preferred way. For example:

```
go run ./cmd/sfu
```

Because the `config.yml` is in the root the app will find them by convention. 
If you put the `config.yml` in another director add the config path on the run command with `go run ./cmd/sfu --config path/config.yml`

---

That's it! Have fun!
