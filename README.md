# JellyTorrent

A BitTorrent client and Kademlia DHT node written in Go with zero external dependencies.

## Usage

```bash
# Build
go build -o /tmp/jellytorrent/ ./...

# Download from a .torrent file
./jellytorrent.sh download -o <destination> <torrent-file>

# Download from a magnet link
./jellytorrent.sh magnet_download -o <destination> <magnet-link>

# Seed a previously downloaded torrent (blocks until Ctrl+C)
./jellytorrent.sh seed -o <storage-dir> <torrent-file>

# Run DHT node via subcommand
./jellytorrent.sh dht -port 6881 -state dht_state.dat

# Or run standalone DHT daemon binary
/tmp/jellytorrent/dhtd -port 6881 -state dht_state.dat

# Enable debug logging
JELLYTORRENT_DEBUG=1 ./jellytorrent.sh download -o <destination> <torrent-file>
```

## Features

- Concurrent piece downloading with rarest-first selection, endgame mode, and TCP pipelining
- .torrent files and magnet links with multi-tracker failover (HTTP/UDP)
- Peer exchange (BEP 11) with dynamic worker spawning
- Full seeding via TCP listener with round-robin choking
- Disk-backed piece storage for download resume
- Kademlia DHT (BEP 5) with standalone daemon mode
- BEP 42 node ID restrictions for Sybil resistance
- Per-IP sliding window rate limiting
- Stale node eviction with per-bucket refresh and ping-on-eviction
- Structured JSON logging and Prometheus metrics endpoint

## Testing

```bash
go test ./...
```