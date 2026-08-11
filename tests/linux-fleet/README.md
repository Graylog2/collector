Fleet Environment
=================

**NOTE:** This probably only works correctly when running on Linux

This Docker Compose project starts a few Collector instances on different
Linux operating systems.

## Setup

1. Copy the `.env.example` file to `.env` and add a real enrollment token.
1. Configure your Graylog server to use `0.0.0.0:9000` as `http_bind_address`
   to ensure that the Collector containers can reach the API.
1. Check that your local firewall allows connections from the container
   network to port `9000` to the container network gateway IP address.

## Run

1. Build the Collector via `go tool task build` in the repository root.
1. Start the compose environment via `docker compose up -d`.
