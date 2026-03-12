package dht

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/krpc"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/routing"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/token"
	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
)

const (
	queryTimeout        = 5 * time.Second
	tokenRotateInterval = 5 * time.Minute
	peerExpireInterval  = 5 * time.Minute
	refreshInterval     = 15 * time.Minute
)

type DHT struct {
	id     nodeid.NodeID
	table  *routing.RoutingTable
	server *krpc.Server
	tokens *token.TokenManager
	peers  *PeerStore
	config Config
	stop   chan struct{}
	wg     sync.WaitGroup
}

func New(opts ...Option) (*DHT, error) {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	id := nodeid.New()
	var savedNodes []*routing.Node

	if config.RoutingTablePath != "" {
		if loadedID, nodes, err := loadRoutingTable(config.RoutingTablePath); err == nil {
			id = loadedID
			savedNodes = nodes
		}
	}

	table := routing.NewRoutingTable(id)
	for _, n := range savedNodes {
		table.Insert(n)
	}

	tokens := token.New()
	peers := NewPeerStore(config.PeerTTL)

	d := &DHT{
		id:     id,
		table:  table,
		tokens: tokens,
		peers:  peers,
		config: config,
		stop:   make(chan struct{}),
	}

	addr, err := netip.ParseAddrPort(fmt.Sprintf("0.0.0.0:%d", config.Port))
	if err != nil {
		return nil, err
	}

	server, err := krpc.NewServer(addr, d.handleQuery)
	if err != nil {
		return nil, err
	}
	d.server = server
	d.server.Start()
	d.startMaintenance()

	return d, nil
}

// startMaintenance launches background goroutines for token rotation,
// peer expiration, and routing table refresh.
func (d *DHT) startMaintenance() {
	// Token rotation: every 5 minutes, rotate the HMAC secret.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(tokenRotateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.tokens.Rotate()
			case <-d.stop:
				return
			}
		}
	}()

	// Peer expiration: every 5 minutes, walk the peer store and remove
	// entries older than the TTL (default 30 min). This prevents stale
	// peers from being returned in get_peers responses. The interval is
	// shorter than the TTL so expired peers are cleaned up promptly.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(peerExpireInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.peers.Expire()
			case <-d.stop:
				return
			}
		}
	}()

	// Routing table refresh: every 15 minutes, perform a find_node lookup
	// on a random ID in each bucket's range. This ensures buckets stay
	// populated even if we haven't organically encountered nodes in that
	// distance range recently. Without this, far-away buckets would slowly
	// empty as nodes go offline and are never replaced.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.refreshTable()
			case <-d.stop:
				return
			}
		}
	}()
}

// refreshTable performs a find_node lookup on a random target to keep
// the routing table populated.
func (d *DHT) refreshTable() {
	target := nodeid.New() // random target
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d.iterativeFindNode(ctx, target)
}

// Bootstrap populates the routing table by contacting bootstrap nodes
// and performing a find_node lookup on our own ID.
func (d *DHT) Bootstrap(ctx context.Context) error {
	for _, addr := range d.config.BootstrapNodes {
		resolved, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		addrPort := resolved.AddrPort()

		resp := d.sendFindNode(ctx, addrPort, d.id)
		if resp == nil {
			continue
		}

		// Insert the bootstrap node itself into our routing table.
		if respID, err := nodeIDFromArgs(resp.Response); err == nil {
			d.table.Insert(&routing.Node{
				ID:       respID,
				Addr:     addrPort,
				LastSeen: time.Now(),
			})
		}

		if nodes, ok := resp.Response["nodes"]; ok {
			for _, n := range parseCompactNodes(nodes) {
				d.table.Insert(n)
			}
		}
	}

	// iterative lookup on our own ID to fill nearby buckets
	d.iterativeFindNode(ctx, d.id)
	return nil
}

// GetPeers performs an iterative lookup for peers downloading the given torrent.
func (d *DHT) GetPeers(ctx context.Context, infoHash [20]byte) ([]netip.AddrPort, error) {
	target := nodeid.NodeID(infoHash)
	closest := d.table.FindClosest(target, routing.K)
	if len(closest) == 0 {
		return nil, fmt.Errorf("no nodes in routing table, run Bootstrap first")
	}

	var (
		mu       sync.Mutex
		allPeers []netip.AddrPort
		queried  = make(map[nodeid.NodeID]bool)
		tokens   = make(map[nodeid.NodeID][20]byte)
	)

	candidates := make([]*routing.Node, len(closest))
	copy(candidates, closest)

	for round := 0; round < 10; round++ {
		var toQuery []*routing.Node
		mu.Lock()
		sort.Slice(candidates, func(i, j int) bool {
			di := target.Distance(candidates[i].ID)
			dj := target.Distance(candidates[j].ID)
			return di.Less(dj)
		})
		for _, c := range candidates {
			if len(toQuery) >= d.config.Alpha {
				break
			}
			if !queried[c.ID] {
				toQuery = append(toQuery, c)
				queried[c.ID] = true
			}
		}
		mu.Unlock()

		if len(toQuery) == 0 {
			break
		}

		var wg sync.WaitGroup
		for _, node := range toQuery {
			wg.Add(1)
			go func(n *routing.Node) {
				defer wg.Done()
				resp := d.sendGetPeers(ctx, n.Addr, infoHash)
				if resp == nil {
					return
				}

				mu.Lock()
				defer mu.Unlock()

				if tok, err := tokenFromResponse(resp.Response); err == nil {
					tokens[n.ID] = tok
				}

				if values, ok := resp.Response["values"]; ok {
					if peerList, ok := values.([]interface{}); ok {
						for _, p := range peerList {
							if addr, err := parseCompactPeer(p); err == nil {
								allPeers = append(allPeers, addr)
							}
						}
					}
				}

				if nodes, ok := resp.Response["nodes"]; ok {
					parsed := parseCompactNodes(nodes)
					candidates = append(candidates, parsed...)
				}
			}(node)
		}
		wg.Wait()

		if len(allPeers) > 0 {
			break
		}
	}

	_ = tokens

	return allPeers, nil
}

// Announce tells the DHT that we are downloading/seeding the given torrent.
func (d *DHT) Announce(ctx context.Context, infoHash [20]byte, port int) error {
	target := nodeid.NodeID(infoHash)
	closest := d.table.FindClosest(target, routing.K)
	if len(closest) == 0 {
		return fmt.Errorf("no nodes in routing table, run Bootstrap first")
	}

	var (
		mu      sync.Mutex
		queried = make(map[nodeid.NodeID]bool)
		tokens  = make(map[nodeid.NodeID][20]byte)
		nodeMap = make(map[nodeid.NodeID]*routing.Node)
	)

	candidates := make([]*routing.Node, len(closest))
	copy(candidates, closest)
	for _, c := range candidates {
		nodeMap[c.ID] = c
	}

	for round := 0; round < 10; round++ {
		var toQuery []*routing.Node
		mu.Lock()
		sort.Slice(candidates, func(i, j int) bool {
			di := target.Distance(candidates[i].ID)
			dj := target.Distance(candidates[j].ID)
			return di.Less(dj)
		})
		for _, c := range candidates {
			if len(toQuery) >= d.config.Alpha {
				break
			}
			if !queried[c.ID] {
				toQuery = append(toQuery, c)
				queried[c.ID] = true
			}
		}
		mu.Unlock()

		if len(toQuery) == 0 {
			break
		}

		var wg sync.WaitGroup
		for _, node := range toQuery {
			wg.Add(1)
			go func(n *routing.Node) {
				defer wg.Done()
				resp := d.sendGetPeers(ctx, n.Addr, infoHash)
				if resp == nil {
					return
				}

				mu.Lock()
				defer mu.Unlock()

				if tok, err := tokenFromResponse(resp.Response); err == nil {
					tokens[n.ID] = tok
				}

				if nodes, ok := resp.Response["nodes"]; ok {
					parsed := parseCompactNodes(nodes)
					for _, p := range parsed {
						nodeMap[p.ID] = p
					}
					candidates = append(candidates, parsed...)
				}
			}(node)
		}
		wg.Wait()
	}

	sort.Slice(candidates, func(i, j int) bool {
		di := target.Distance(candidates[i].ID)
		dj := target.Distance(candidates[j].ID)
		return di.Less(dj)
	})

	announced := 0
	for _, c := range candidates {
		if announced >= routing.K {
			break
		}
		tok, ok := tokens[c.ID]
		if !ok {
			continue
		}
		d.sendAnnouncePeer(ctx, c.Addr, infoHash, port, tok)
		announced++
	}

	return nil
}

// Close shuts down the DHT: stops maintenance goroutines, waits for them
// to finish, then closes the UDP server.
func (d *DHT) Close() error {
	close(d.stop)
	d.wg.Wait()
	return d.server.Close()
}

// Save serializes the routing table to disk at the given path.
// Uses atomic write (tmp file + rename) to prevent corruption.
func (d *DHT) Save(path string) error {
	nodes := d.table.Snapshot()
	dict := map[string]interface{}{
		"id":    string(d.id[:]),
		"nodes": compactNodes(nodes),
	}
	data, err := bencode.Encode(dict)
	if err != nil {
		return fmt.Errorf("encode routing table: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write routing table: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename routing table: %w", err)
	}
	return nil
}

// loadRoutingTable reads a persisted routing table from disk.
func loadRoutingTable(path string) (nodeid.NodeID, []*routing.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nodeid.NodeID{}, nil, err
	}

	decoded, err := bencode.Decode(data)
	if err != nil {
		return nodeid.NodeID{}, nil, fmt.Errorf("decode routing table: %w", err)
	}

	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nodeid.NodeID{}, nil, fmt.Errorf("routing table is not a dict")
	}

	id, err := bytesFromArgs(dict, "id")
	if err != nil {
		return nodeid.NodeID{}, nil, fmt.Errorf("routing table missing id: %w", err)
	}

	nodes := parseCompactNodes(dict["nodes"])
	return id, nodes, nil
}

// --- Iterative lookup ---

func (d *DHT) iterativeFindNode(ctx context.Context, target nodeid.NodeID) []*routing.Node {
	closest := d.table.FindClosest(target, routing.K)
	if len(closest) == 0 {
		return nil
	}

	queried := make(map[nodeid.NodeID]bool)
	candidates := make([]*routing.Node, len(closest))
	copy(candidates, closest)

	for round := 0; round < 10; round++ {
		sort.Slice(candidates, func(i, j int) bool {
			di := target.Distance(candidates[i].ID)
			dj := target.Distance(candidates[j].ID)
			return di.Less(dj)
		})

		var toQuery []*routing.Node
		for _, c := range candidates {
			if len(toQuery) >= d.config.Alpha {
				break
			}
			if !queried[c.ID] {
				toQuery = append(toQuery, c)
				queried[c.ID] = true
			}
		}

		if len(toQuery) == 0 {
			break
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, node := range toQuery {
			wg.Add(1)
			go func(n *routing.Node) {
				defer wg.Done()
				resp := d.sendFindNode(ctx, n.Addr, target)
				if resp == nil {
					return
				}
				if nodes, ok := resp.Response["nodes"]; ok {
					parsed := parseCompactNodes(nodes)
					mu.Lock()
					candidates = append(candidates, parsed...)
					mu.Unlock()
				}
			}(node)
		}
		wg.Wait()
	}

	sort.Slice(candidates, func(i, j int) bool {
		di := target.Distance(candidates[i].ID)
		dj := target.Distance(candidates[j].ID)
		return di.Less(dj)
	})
	if len(candidates) > routing.K {
		candidates = candidates[:routing.K]
	}
	return candidates
}

// --- Outgoing queries ---

func (d *DHT) sendFindNode(ctx context.Context, addr netip.AddrPort, target nodeid.NodeID) *krpc.Message {
	msg := &krpc.Message{
		Type:        "q",
		QueryMethod: "find_node",
		Args: map[string]any{
			"id":     string(d.id[:]),
			"target": string(target[:]),
		},
	}
	return d.sendQuery(ctx, msg, addr)
}

func (d *DHT) sendGetPeers(ctx context.Context, addr netip.AddrPort, infoHash [20]byte) *krpc.Message {
	msg := &krpc.Message{
		Type:        "q",
		QueryMethod: "get_peers",
		Args: map[string]any{
			"id":        string(d.id[:]),
			"info_hash": string(infoHash[:]),
		},
	}
	return d.sendQuery(ctx, msg, addr)
}

func (d *DHT) sendAnnouncePeer(ctx context.Context, addr netip.AddrPort, infoHash [20]byte, port int, tok [20]byte) *krpc.Message {
	msg := &krpc.Message{
		Type:        "q",
		QueryMethod: "announce_peer",
		Args: map[string]any{
			"id":           string(d.id[:]),
			"info_hash":    string(infoHash[:]),
			"port":         port,
			"token":        string(tok[:]),
			"implied_port": 0,
		},
	}
	return d.sendQuery(ctx, msg, addr)
}

// sendQuery sends a query and waits for the response with a timeout.
// Cancels the transaction if no response arrives in time.
func (d *DHT) sendQuery(ctx context.Context, msg *krpc.Message, addr netip.AddrPort) *krpc.Message {
	txnID, ch, err := d.server.Send(msg, addr)
	if err != nil {
		return nil
	}

	select {
	case resp := <-ch:
		return resp
	case <-ctx.Done():
		d.server.Cancel(txnID)
		return nil
	case <-time.After(queryTimeout):
		d.server.Cancel(txnID)
		return nil
	}
}

// --- Query handlers ---

func (d *DHT) handleQuery(msg *krpc.Message, addr netip.AddrPort) {
	senderID, err := nodeIDFromArgs(msg.Args)
	if err != nil {
		return
	}
	d.table.Insert(&routing.Node{
		ID:       senderID,
		Addr:     addr,
		LastSeen: time.Now(),
	})

	switch msg.QueryMethod {
	case "ping":
		d.handlePing(msg, addr)
	case "find_node":
		d.handleFindNode(msg, addr)
	case "get_peers":
		d.handleGetPeers(msg, addr)
	case "announce_peer":
		d.handleAnnouncePeer(msg, addr)
	}
}

func (d *DHT) handlePing(msg *krpc.Message, addr netip.AddrPort) {
	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		Response: map[string]any{
			"id": string(d.id[:]),
		},
	}
	d.server.Reply(resp, addr)
}

func (d *DHT) handleFindNode(msg *krpc.Message, addr netip.AddrPort) {
	target, err := targetFromArgs(msg.Args)
	if err != nil {
		return
	}

	closest := d.table.FindClosest(target, routing.K)

	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		Response: map[string]any{
			"id":    string(d.id[:]),
			"nodes": compactNodes(closest),
		},
	}
	d.server.Reply(resp, addr)
}

func (d *DHT) handleGetPeers(msg *krpc.Message, addr netip.AddrPort) {
	infoHash, err := infoHashFromArgs(msg.Args)
	if err != nil {
		return
	}

	tok := d.tokens.Generate(addr.Addr())
	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		Response: map[string]any{
			"id":    string(d.id[:]),
			"token": string(tok[:]),
		},
	}

	peers := d.peers.Get(infoHash)
	if len(peers) > 0 {
		resp.Response["values"] = compactPeers(peers)
	} else {
		closest := d.table.FindClosest(nodeid.NodeID(infoHash), routing.K)
		resp.Response["nodes"] = compactNodes(closest)
	}

	d.server.Reply(resp, addr)
}

func (d *DHT) handleAnnouncePeer(msg *krpc.Message, addr netip.AddrPort) {
	infoHash, err := infoHashFromArgs(msg.Args)
	if err != nil {
		return
	}

	tok, err := tokenFromArgs(msg.Args)
	if err != nil {
		return
	}

	if !d.tokens.Validate(addr.Addr(), tok) {
		d.server.Reply(&krpc.Message{
			TransactionID: msg.TransactionID,
			Type:          "e",
			Error:         []any{203, "bad token"},
		}, addr)
		return
	}

	peerAddr := addr
	if impliedPort, ok := msg.Args["implied_port"]; !ok || impliedPort != 1 {
		port, ok := msg.Args["port"].(int)
		if !ok {
			return
		}
		peerAddr = netip.AddrPortFrom(addr.Addr(), uint16(port))
	}

	d.peers.Add(infoHash, peerAddr)

	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		Response: map[string]any{
			"id": string(d.id[:]),
		},
	}
	d.server.Reply(resp, addr)
}

// --- Argument extraction helpers ---

func nodeIDFromArgs(args map[string]any) (nodeid.NodeID, error) {
	return bytesFromArgs(args, "id")
}

func targetFromArgs(args map[string]any) (nodeid.NodeID, error) {
	return bytesFromArgs(args, "target")
}

func bytesFromArgs(args map[string]any, key string) (nodeid.NodeID, error) {
	raw, ok := args[key]
	if !ok {
		return nodeid.NodeID{}, fmt.Errorf("missing %s", key)
	}
	switch v := raw.(type) {
	case string:
		return nodeid.FromBytes([]byte(v))
	case []byte:
		return nodeid.FromBytes(v)
	default:
		return nodeid.NodeID{}, fmt.Errorf("invalid %s type: %T", key, raw)
	}
}

func infoHashFromArgs(args map[string]any) ([20]byte, error) {
	return fixed20FromArgs(args, "info_hash")
}

func tokenFromArgs(args map[string]any) ([20]byte, error) {
	return fixed20FromArgs(args, "token")
}

func tokenFromResponse(resp map[string]any) ([20]byte, error) {
	return fixed20FromArgs(resp, "token")
}

func fixed20FromArgs(args map[string]any, key string) ([20]byte, error) {
	raw, ok := args[key]
	if !ok {
		return [20]byte{}, fmt.Errorf("missing %s", key)
	}
	var b []byte
	switch v := raw.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return [20]byte{}, fmt.Errorf("invalid %s type: %T", key, raw)
	}
	if len(b) != 20 {
		return [20]byte{}, fmt.Errorf("%s must be 20 bytes", key)
	}
	var out [20]byte
	copy(out[:], b)
	return out, nil
}

// --- Compact encoding/decoding ---

func compactNodes(nodes []*routing.Node) string {
	buf := make([]byte, 26*len(nodes))
	for i, n := range nodes {
		off := i * 26
		copy(buf[off:off+20], n.ID[:])
		ip := n.Addr.Addr().As4()
		copy(buf[off+20:off+24], ip[:])
		binary.BigEndian.PutUint16(buf[off+24:off+26], n.Addr.Port())
	}
	return string(buf)
}

func compactPeers(addrs []netip.AddrPort) []any {
	peers := make([]any, len(addrs))
	for i, a := range addrs {
		var buf [6]byte
		ip := a.Addr().As4()
		copy(buf[:4], ip[:])
		binary.BigEndian.PutUint16(buf[4:6], a.Port())
		peers[i] = string(buf[:])
	}
	return peers
}

func parseCompactNodes(raw any) []*routing.Node {
	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return nil
	}
	if len(data)%26 != 0 {
		return nil
	}

	nodes := make([]*routing.Node, 0, len(data)/26)
	for i := 0; i+26 <= len(data); i += 26 {
		id, err := nodeid.FromBytes(data[i : i+20])
		if err != nil {
			continue
		}
		ip := netip.AddrFrom4([4]byte(data[i+20 : i+24]))
		port := binary.BigEndian.Uint16(data[i+24 : i+26])
		nodes = append(nodes, &routing.Node{
			ID:       id,
			Addr:     netip.AddrPortFrom(ip, port),
			LastSeen: time.Now(),
		})
	}
	return nodes
}

func parseCompactPeer(raw any) (netip.AddrPort, error) {
	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return netip.AddrPort{}, fmt.Errorf("invalid peer type: %T", raw)
	}
	if len(data) != 6 {
		return netip.AddrPort{}, fmt.Errorf("peer must be 6 bytes")
	}
	ip := netip.AddrFrom4([4]byte(data[:4]))
	port := binary.BigEndian.Uint16(data[4:6])
	return netip.AddrPortFrom(ip, port), nil
}
