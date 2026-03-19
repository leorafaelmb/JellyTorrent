# JellyTorrent

A BitTorrent client and Kademlia DHT node written in Go with zero external dependencies.

Built to the [BitTorrent Protocol Specification (BEP 3)](https://www.bittorrent.org/beps/bep_0003.html) and [DHT Protocol (BEP 5)](https://www.bittorrent.org/beps/bep_0005.html).

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

### Core Protocol (BEP 3)
- Peer wire protocol: handshake, choke/unchoke, interested/not-interested, have, bitfield, request, piece, cancel
- TCP pipelining with up to 5 concurrent block requests per peer
- SHA-1 piece verification
- Concurrent download across a configurable worker pool (default 50 workers)

### Piece Selection
- **Rarest-first** (default) — prioritizes pieces held by the fewest peers, with random tie-breaking
- **Sequential** — downloads pieces in order, available as a fallback
- **Endgame mode** — when few pieces remain, multiple workers download the same pieces simultaneously; cancel messages are sent when a duplicate piece completes

### DHT (BEP 5)
- Full Kademlia distributed hash table for trackerless peer discovery
- Bootstrap, peer lookup (`get_peers`), and announce (`announce_peer`)
- K-bucket routing table with node eviction and refresh
- KRPC protocol over UDP
- Routing table persistence across restarts
- Standalone DHT daemon (`dhtd`) for running an independent node

### Tracker Support
- HTTP and UDP tracker protocols (announce and scrape)
- **Multi-tracker fallback** (BEP 12) — tries trackers in order from `announce-list`; falls back to the next on failure
- **Tracker lifecycle** — sends `started`, periodic re-announces at the tracker's interval, `completed`, and `stopped` events

### Peer Exchange (BEP 11)
- PEX as a third peer discovery channel alongside trackers and DHT
- Negotiated via extension handshake (BEP 10) — advertises `ut_pex` alongside `ut_metadata`
- Delta-based: periodically sends added/dropped peers in compact format (every 60 seconds)
- Dynamically spawns new download workers for PEX-discovered peers
- Both downloader (leeching) and seeder participate in PEX

### File Support
- `.torrent` file parsing (single-file and multi-file torrents)
- Magnet link support with metadata download via the extension protocol (BEP 10)
- Multi-tracker magnet links (multiple `tr` parameters)

### Upload & Seeding
- Serves piece data to connected peers during download (leeching upload)
- Responds to incoming `Request` messages inline across all message loops
- Broadcasts `Have` messages to connected peers as pieces complete
- **Full seeding** via TCP listener for incoming connections (`seed` subcommand)
- Round-robin choking algorithm with configurable unchoke slots
- Disk-backed piece storage for download resume and seeding from disk

### Download Resume
- Completed pieces are persisted to disk, enabling resume on restart
- Bitfield file tracks which pieces are present for fast startup

## Architecture

```
cmd/
  bittorrent/              CLI entry point and command dispatch
  dhtd/                    Standalone DHT daemon
internal/
  bencode/                 Bencode encoder/decoder
  dht/                     Kademlia DHT (BEP 5)
    krpc/                  KRPC protocol messages
    nodeid/                Node ID generation and XOR distance
    routing/               K-bucket routing table
    token/                 Announce token management
  metainfo/                .torrent and magnet link parsing, piece hashing
  tracker/                 Tracker interface: HTTP, UDP, multi-tracker
  peer/                    Peer wire protocol, handshake, block pipelining, uploads, PEX
  downloader/              Download orchestrator, worker pool, piece manager, PEX manager
  seeder/                  TCP listener, choking algorithm, seed peer management
  storage/                 Disk-backed piece storage for download resume
  logger/                  Structured logging via slog
```

## Testing

```bash
go test ./...
```

Sample `.torrent` files for testing are in the `torrents/` directory.

## TODO

- **Encryption** (MSE/PE) — message stream encryption for ISP throttling resistance
- **uTP** (BEP 29) — UDP-based transport with built-in congestion control
- **Persistent seeding daemon** — auto-start on boot, multi-torrent, launchd integration
- **Bandwidth management** — upload/download rate limiting

## License

MIT
