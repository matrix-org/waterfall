# SFU-to-SFU

## Why

`SFU-to-SFU` is an example of a cascaded decentralised SFU. The intention is to
be a implementation of Matrix's [MSC3401: Native Group VoIP
signalling](https://github.com/matrix-org/matrix-spec-proposals/blob/matthew/group-voip/proposals/3401-group-voip.md).
This example is self contained and doesn't require any external software. The
project was informed by the following goals.

* **Easy Scaling** - SFU count can be grown/shrunk as users arrive. We don't
  scale on the dimension of calls making things easier.
* **Shorter Last Mile** - Users can connect to SFUs closest to them. Links `SFU
  <-> SFU` are higher quality then public hops.
* **Flexibility in WebRTC server choice** - All communication takes place using
  standard protocols/formats. You can use whatever server software best fits
  your needs.
* **Client Simplicity** - Clients will need to be created on lots of platforms.
  We should aim to use native WebRTC features as much as possible.

This implements the MSC only roughly - given the current experimental nature of
this projects, it deviates in certain areas from the MSC.

## How

### Configuration

* `cp config.yaml.sample config.yaml`
* Fill in `config.yaml`

### Running

* `./scripts/run.sh`
* Access at <http://localhost:8080>

### Profiling

* `./scripts/profile.sh`
* Access at <http://localhost:8080>

### Building

* `./scripts/build.sh`
* `./dist/bin`
* Access at <http://localhost:8080>
