# mtsh

mtsh is a command-line tool that provides a remote shell over Meshtastic text messages. It pairs with a Meshtastic device (serial, BLE, or HTTP), runs shell commands received over the mesh, and streams the output back in chunked text responses.

## Features
- **Remote execution server**: Accept Meshtastic messages containing shell commands and return their output.
- **Terminal proxy client**: Send commands to a remote node and print responses in your local terminal.
- **Chunked messaging**: Splits large outputs into multiple text message chunks with optional acknowledgements, retries, and progress reporting.
- **Adaptive compression**: Compresses payloads when it reduces size to fit more data per message.
- **Batched acknowledgements**: Confirms multiple chunks together for reliable transfers with fewer round trips.
- **Filtering and safety controls**: Configure channel usage, direct-message-only mode, and whitelist allowed node IDs.

## Requirements
- Go 1.24 or later (see `go.mod`).
- Access to a Meshtastic device via serial port, BLE, or HTTP API.

## Installation

### From source
```bash
go build -o mtsh main.go
```
This produces the `mtsh` binary in the current directory.

### Install from prebuilt binary

Download the binary for your platform from the latest release:
https://github.com/playmean/mtsh/releases/latest

Example:
```bash
curl -LO https://github.com/playmean/mtsh/releases/latest/download/mtsh-X.Y.Z-linux-amd64
chmod +x mtsh-X.Y.Z-linux-amd64
sudo mv mtsh-X.Y.Z-linux-amd64 /usr/local/bin/mtsh
mtsh version
```

## Usage
Most commands require exactly one of `--channel` or `--to`; using both together is not supported. The `nodes` command needs neither. Common flags include `--port`, `--chunk-bytes`, `--hop-limit`, and verbosity controls. If `--port` is omitted, mtsh attaches to the first compatible serial device. Use `-h` for full command help.
To connect over the Meshtastic HTTP API, pass `--port http://host:port` (or `https://...`).

### Start a server
Run on the device that should execute shell commands.

Recommended direct-message-only startup (restrict execution to known nodes):
```bash
./mtsh server --dm-only --dm-whitelist 0x12345678,0x23456789
```

Optional broadcast-on-channel startup (for users who understand the risks):
```bash
./mtsh server --port /dev/ttyUSB0 --channel 1 --cmd-timeout 20s
```
Useful flags for either mode:
- `--no-chunk-ack` to disable chunk acknowledgements if you prefer timed delays.
- `--allow-plain` to accept plain text commands (see below).

#### Plain mode
Use `--allow-plain` when integrating with tools or apps that sends plain text messages. In this mode:
- Incoming non-empty text packets are treated as shell commands without extra flags or prefixes.
- Responses are returned as plain text chunks instead of proto-formatted messages.
- When an output spans multiple chunks, each chunk starts with `N/M` (for example `1/5`) so receivers can reassemble the stream.
- Chunk delays follow `--chunk-delay`, and no acknowledgements or promos are sent; the server simply transmits the chunks with the configured pause.

### Run in Docker
Run the server in a container with a USB serial device passed through:
```bash
docker run --rm -it --device /dev/ttyUSB0 ghcr.io/playmean/mtsh:latest server -v --dm-only --dm-whitelist 0x12345678
```

### Run a client
Use from a node that should send commands and collect output:
```bash
./mtsh client --port /dev/ttyUSB1 --to 0x12345678 --wait-timeout 2m "uname -a"
```
Optional flags:
- `--stream-chunks` to print each chunk as it arrives (disables buffering/progress).
- `--no-progress` to hide buffered download progress.

### Copy a file to the server
Send a local file to the remote shell host:
```bash
./mtsh cp ./bin/tool /tmp/tool --port /dev/ttyUSB1 --to 0x12345678
```
Notes:
- Respects `--chunk-bytes` and `--no-chunk-ack` to tune transfer behaviour.
- Compresses files automatically when it shrinks the payload.

### Send a plain message
Broadcast or direct-message a node without running the shell:
```bash
./mtsh msg "hello mesh!" --port /dev/ttyUSB0 --to 0xabcdef01
```

### List known nodes
Display nodes stored on the connected device, including names and last-heard info:
```bash
./mtsh nodes --port /dev/ttyUSB0
```

## Logging
Enable verbose debug logging with `-v` or `--verbose` on any command to inspect device connection details, chunking behaviour, and acknowledgements.

## Development
Standard Go tooling works with this repository. Run `go fmt ./...` and `go test ./...` before committing changes where applicable.
