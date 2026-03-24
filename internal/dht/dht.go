package dht

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/bencode"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/hardening"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/krpc"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/metrics"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/nodeid"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/routing"
	"github.com/leorafaelmb/JellyTorrent/internal/dht/token"
)

const (
	queryTimeout        = 2 * time.Second
	tokenRotateInterval = 5 * time.Minute
	peerExpireInterval  = 5 * time.Minute
	refreshInterval     = 15 * time.Minute
	saveInterval        = 5 * time.Minute
)

type DHT struct {
	id         nodeid.NodeID
	table      *routing.RoutingTable
	server     *krpc.Server
	tokens     *token.TokenManager
	peers      *PeerStore
	config     Config
	stop       chan struct{}
	wg         sync.WaitGroup
	externalIP  netip.Addr              // BEP 42: our external IP as seen by others
	ipVotes     map[netip.Addr]int    // BEP 42: IP votes from bootstrap responses
	ipVotesMu   sync.Mutex
	rateLimiter  *hardening.RateLimiter // nil if rate limiting disabled
	metrics      *metrics.Metrics      // nil if metrics disabled
	metricsHTTP  *http.Server          // nil if metrics disabled
}

func New(opts ...Option) (*DHT, error) {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	id := nodeid.New()
	var savedNodes []*routing.Node
	var savedIP netip.Addr

	if config.RoutingTablePath != "" {
		if state, err := loadRoutingTable(config.RoutingTablePath); err == nil {
			id = state.id
			savedNodes = state.nodes
			savedIP = state.externalIP
		}
	}

	table := routing.NewRoutingTable(id, config.Logger)
	for _, n := range savedNodes {
		table.Insert(n)
	}

	tokens := token.New()
	peers := NewPeerStore(config.PeerTTL)

	var rl *hardening.RateLimiter
	if config.RateLimit > 0 {
		rl = hardening.NewRateLimiter(config.RateLimit, config.RateLimitWin, 100000)
	}

	d := &DHT{
		id:          id,
		table:       table,
		tokens:      tokens,
		peers:       peers,
		config:      config,
		stop:        make(chan struct{}),
		ipVotes:     make(map[netip.Addr]int),
		externalIP:  savedIP,
		rateLimiter: rl,
	}

	addr, err := netip.ParseAddrPort(fmt.Sprintf("0.0.0.0:%d", config.Port))
	if err != nil {
		return nil, err
	}

	server, err := krpc.NewServer(addr, d.handleQuery, config.Logger)
	if err != nil {
		return nil, err
	}
	d.server = server
	d.server.Start()
	d.startMaintenance()

	// Start metrics HTTP server if configured.
	if config.MetricsPort > 0 {
		rateLimitedIPs := func() int { return 0 }
		if d.rateLimiter != nil {
			rateLimitedIPs = d.rateLimiter.Len
		}
		d.metrics = metrics.NewMetrics(
			d.table.NumNodes,
			d.peers.Count,
			rateLimitedIPs,
		)
		mux := http.NewServeMux()
		mux.Handle("/metrics", d.metrics.Handler())
		d.metricsHTTP = &http.Server{
			Addr:    fmt.Sprintf(":%d", config.MetricsPort),
			Handler: mux,
		}
		go func() {
			if err := d.metricsHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				config.Logger.Warn("metrics HTTP server error", "err", err)
			}
		}()
		config.Logger.Info("metrics endpoint started", "port", config.MetricsPort)
	}

	config.Logger.Info("DHT node started",
		"node_id", d.id.String(),
		"port", d.server.LocalAddr().Port(),
		"table_size", d.table.NumNodes(),
	)

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
				d.config.Logger.Debug("token rotated")
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
				expired := d.peers.Expire()
				if expired > 0 {
					d.config.Logger.Debug("peer store cleanup", "expired_count", expired)
				}
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

	// Periodic routing table save: every 5 minutes, persist the routing
	// table to disk so progress is not lost on crashes or panics.
	if d.config.RoutingTablePath != "" {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			ticker := time.NewTicker(saveInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := d.Save(d.config.RoutingTablePath); err != nil {
						d.config.Logger.Warn("periodic routing table save failed", "err", err)
					} else {
						d.config.Logger.Debug("routing table saved", "path", d.config.RoutingTablePath, "table_size", d.table.NumNodes())
					}
				case <-d.stop:
					return
				}
			}
		}()
	}

	// Rate limiter cleanup: periodically sweep expired per-IP counters.
	if d.rateLimiter != nil {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			ticker := time.NewTicker(d.config.RateLimitWin)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					removed := d.rateLimiter.Cleanup()
					if removed > 0 {
						d.config.Logger.Debug("rate limiter cleanup",
							"removed_count", removed,
							"remaining_count", d.rateLimiter.Len(),
						)
					}
				case <-d.stop:
					return
				}
			}
		}()
	}
}

// refreshTable performs find_node lookups targeting stale buckets to keep
// the routing table populated, then sweeps stale nodes.
func (d *DHT) refreshTable() {
	staleBuckets := d.table.StaleBuckets(refreshInterval)
	if len(staleBuckets) == 0 {
		d.sweepStaleNodes()
		return
	}
	d.config.Logger.Debug("refreshing stale buckets", "count", len(staleBuckets))
	for _, bucket := range staleBuckets {
		target := d.table.TargetForBucket(bucket)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		d.iterativeFindNode(ctx, target)
		cancel()
	}
	d.sweepStaleNodes()
}

// replaceIfDead pings the oldest node in a full bucket. If it doesn't
// respond, evicts it and inserts the replacement. Called asynchronously
// from handleQuery when a new node is rejected from a full bucket.
func (d *DHT) replaceIfDead(oldest, replacement *routing.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	resp := d.sendPing(ctx, oldest.Addr)
	if resp == nil {
		d.table.Remove(oldest)
		d.table.Insert(replacement)
	}
}

// sweepStaleNodes pings all nodes that haven't responded recently and
// records failures for unresponsive ones, triggering eviction at MaxFailures.
func (d *DHT) sweepStaleNodes() {
	stale := d.table.Stale(refreshInterval)
	if len(stale) == 0 {
		return
	}
	d.config.Logger.Debug("stale node sweep started", "stale_count", len(stale))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, n := range stale {
		resp := d.sendPing(ctx, n.Addr)
		if resp == nil {
			d.table.RecordFailure(n.ID)
		} else {
			d.table.RecordSuccess(n.ID)
		}
	}
}

// Bootstrap populates the routing table by contacting bootstrap nodes
// and performing a find_node lookup on our own ID. When BEP 42 is enabled,
// it collects external IP votes from responses and regenerates the node ID
// to be compliant before performing the iterative lookup.
func (d *DHT) Bootstrap(ctx context.Context) error {
	start := time.Now()
	d.config.Logger.Info("bootstrap started",
		"bootstrap_nodes", len(d.config.BootstrapNodes),
	)

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

		// BEP 42: extract external IP from response and vote.
		if d.config.BEP42 != BEP42Off && resp.IP != nil {
			if extAddr, ok := parseCompactAddr(resp.IP); ok {
				if consensus, ok := d.voteExternalIP(extAddr.Addr()); ok && !d.externalIP.IsValid() {
					d.regenerateCompliantID(consensus)
				}
			}
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

	bootstrapElapsed := time.Since(start)
	d.config.Logger.Info("bootstrap complete",
		"duration_ms", bootstrapElapsed.Milliseconds(),
		"table_size", d.table.NumNodes(),
	)
	if d.metrics != nil {
		d.metrics.SetBootstrapDuration(bootstrapElapsed.Seconds())
	}

	return nil
}

// GetPeers performs an iterative lookup for peers downloading the given torrent.
func (d *DHT) GetPeers(ctx context.Context, infoHash [20]byte) ([]netip.AddrPort, error) {
	start := time.Now()
	ihHex := hex.EncodeToString(infoHash[:])
	d.config.Logger.Debug("get_peers started", "info_hash", ihHex)

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
					d.table.RecordFailure(n.ID)
					return
				}
				d.table.RecordSuccess(n.ID)

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

	d.config.Logger.Info("get_peers complete",
		"info_hash", ihHex,
		"duration_ms", time.Since(start).Milliseconds(),
		"peers_found", len(allPeers),
	)

	return allPeers, nil
}

// Announce tells the DHT that we are downloading/seeding the given torrent.
func (d *DHT) Announce(ctx context.Context, infoHash [20]byte, port int) error {
	start := time.Now()
	ihHex := hex.EncodeToString(infoHash[:])
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
					d.table.RecordFailure(n.ID)
					return
				}
				d.table.RecordSuccess(n.ID)

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

	d.config.Logger.Info("announce complete",
		"info_hash", ihHex,
		"duration_ms", time.Since(start).Milliseconds(),
		"announced_to", announced,
	)

	return nil
}

// Close shuts down the DHT: stops maintenance goroutines, waits for them
// to finish, then closes the UDP server.
func (d *DHT) Close() error {
	d.config.Logger.Info("DHT node stopping",
		"node_id", d.id.String(),
		"table_size", d.table.NumNodes(),
	)
	if d.metricsHTTP != nil {
		d.metricsHTTP.Close()
	}
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

	// BEP 42: persist external IP so the saved ID can be validated on reload.
	if d.externalIP.IsValid() {
		ip4 := d.externalIP.As4()
		dict["external_ip"] = string(ip4[:])
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

// savedState holds the deserialized routing table data from disk.
type savedState struct {
	id         nodeid.NodeID
	nodes      []*routing.Node
	externalIP netip.Addr
}

// loadRoutingTable reads a persisted routing table from disk.
func loadRoutingTable(path string) (*savedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	decoded, err := bencode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode routing table: %w", err)
	}

	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("routing table is not a dict")
	}

	id, err := bytesFromArgs(dict, "id")
	if err != nil {
		return nil, fmt.Errorf("routing table missing id: %w", err)
	}

	state := &savedState{
		id:    id,
		nodes: parseCompactNodes(dict["nodes"]),
	}

	// BEP 42: restore external IP if present.
	if raw, ok := dict["external_ip"]; ok {
		var ipBytes []byte
		switch v := raw.(type) {
		case string:
			ipBytes = []byte(v)
		case []byte:
			ipBytes = v
		}
		if len(ipBytes) == 4 {
			state.externalIP = netip.AddrFrom4([4]byte(ipBytes))
		}
	}

	return state, nil
}

// --- Iterative lookup ---

func (d *DHT) iterativeFindNode(ctx context.Context, target nodeid.NodeID) []*routing.Node {
	start := time.Now()
	d.config.Logger.Debug("lookup started", "target", target.String())

	closest := d.table.FindClosest(target, routing.K)
	if len(closest) == 0 {
		return nil
	}

	queried := make(map[nodeid.NodeID]bool)
	candidates := make([]*routing.Node, len(closest))
	copy(candidates, closest)

	var rounds int
	var prevClosest nodeid.NodeID
	for rounds = 0; rounds < 10; rounds++ {
		sort.Slice(candidates, func(i, j int) bool {
			di := target.Distance(candidates[i].ID)
			dj := target.Distance(candidates[j].ID)
			return di.Less(dj)
		})

		// Check if the closest node improved since last round.
		if rounds > 0 && candidates[0].ID == prevClosest {
			break // converged — no closer nodes found
		}
		prevClosest = candidates[0].ID

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
					d.table.RecordFailure(n.ID)
					return
				}
				d.table.RecordSuccess(n.ID)
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

	elapsed := time.Since(start)
	d.config.Logger.Info("lookup complete",
		"target", target.String(),
		"duration_ms", elapsed.Milliseconds(),
		"nodes_found", len(candidates),
		"rounds", rounds,
	)
	if d.metrics != nil {
		d.metrics.LookupDuration.Observe(elapsed.Seconds())
	}

	return candidates
}

// --- Outgoing queries ---

func (d *DHT) sendPing(ctx context.Context, addr netip.AddrPort) *krpc.Message {
	msg := &krpc.Message{
		Type:        "q",
		QueryMethod: "ping",
		Args: map[string]any{
			"id": string(d.id[:]),
		},
	}
	return d.sendQuery(ctx, msg, addr, "ping")
}

func (d *DHT) sendFindNode(ctx context.Context, addr netip.AddrPort, target nodeid.NodeID) *krpc.Message {
	msg := &krpc.Message{
		Type:        "q",
		QueryMethod: "find_node",
		Args: map[string]any{
			"id":     string(d.id[:]),
			"target": string(target[:]),
		},
	}
	return d.sendQuery(ctx, msg, addr, "find_node")
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
	return d.sendQuery(ctx, msg, addr, "get_peers")
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
	return d.sendQuery(ctx, msg, addr, "announce_peer")
}

// sendQuery sends a query and waits for the response with a timeout.
// Cancels the transaction if no response arrives in time.
func (d *DHT) sendQuery(ctx context.Context, msg *krpc.Message, addr netip.AddrPort, method string) *krpc.Message {
	start := time.Now()
	if d.metrics != nil {
		if idx := metrics.MethodIndex(method); idx >= 0 {
			d.metrics.QueriesOutbound[idx].Inc()
		}
	}
	txnID, ch, err := d.server.Send(msg, addr)
	if err != nil {
		d.config.Logger.Debug("query send failed",
			"method", method,
			"addr", formatAddr(addr),
			"err", err,
		)
		return nil
	}

	select {
	case resp := <-ch:
		d.config.Logger.Debug("query response",
			"method", method,
			"addr", formatAddr(addr),
			"duration_ms", time.Since(start).Milliseconds(),
			"status", "success",
		)
		if d.metrics != nil {
			d.metrics.ResponseSuccess.Inc()
		}
		return resp
	case <-ctx.Done():
		d.server.Cancel(txnID)
		d.config.Logger.Debug("query response",
			"method", method,
			"addr", formatAddr(addr),
			"duration_ms", time.Since(start).Milliseconds(),
			"status", "ctx_canceled",
		)
		if d.metrics != nil {
			d.metrics.ResponseCanceled.Inc()
		}
		return nil
	case <-time.After(queryTimeout):
		d.server.Cancel(txnID)
		d.config.Logger.Debug("query response",
			"method", method,
			"addr", formatAddr(addr),
			"duration_ms", time.Since(start).Milliseconds(),
			"status", "timeout",
		)
		if d.metrics != nil {
			d.metrics.ResponseTimeout.Inc()
		}
		return nil
	}
}

// --- Query handlers ---

func (d *DHT) handleQuery(msg *krpc.Message, addr netip.AddrPort) {
	if d.rateLimiter != nil && !d.rateLimiter.Allow(addr.Addr()) {
		d.config.Logger.Debug("rate limited query",
			"addr", formatAddr(addr),
			"method", msg.QueryMethod,
		)
		if d.metrics != nil {
			d.metrics.RateLimited.Inc()
		}
		return
	}

	senderID, err := nodeIDFromArgs(msg.Args)
	if err != nil {
		return
	}

	compliant := true
	if d.config.BEP42 != BEP42Off {
		compliant = hardening.ValidateNodeID(senderID, addr.Addr())
		if !compliant {
			d.config.Logger.Debug("BEP 42 non-compliant node",
				"id", senderID.String(),
				"addr", formatAddr(addr),
			)
			if d.metrics != nil {
				d.metrics.BEP42NonCompliant.Inc()
			}
			if d.config.BEP42 == BEP42Enforce {
				return // drop query from non-compliant node
			}
		}
	}

	newNode := &routing.Node{
		ID:        senderID,
		Addr:      addr,
		LastSeen:  time.Now(),
		Compliant: compliant,
	}
	oldest, ok := d.table.Insert(newNode)
	if !ok && oldest != nil {
		go d.replaceIfDead(oldest, newNode)
	}

	d.config.Logger.Debug("query received",
		"method", msg.QueryMethod,
		"addr", formatAddr(addr),
		"node_id", senderID.String(),
		"direction", "inbound",
	)
	if d.metrics != nil {
		if idx := metrics.MethodIndex(msg.QueryMethod); idx >= 0 {
			d.metrics.QueriesInbound[idx].Inc()
		}
	}

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
		IP:            compactAddr(addr),
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

	d.config.Logger.Debug("find_node target",
		"addr", formatAddr(addr),
		"target", target.String(),
	)

	closest := d.table.FindClosest(target, routing.K)

	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		IP:            compactAddr(addr),
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

	d.config.Logger.Debug("get_peers info_hash",
		"addr", formatAddr(addr),
		"info_hash", hex.EncodeToString(infoHash[:]),
	)

	tok := d.tokens.Generate(addr.Addr())
	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		IP:            compactAddr(addr),
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
		d.config.Logger.Debug("token validation failed",
			"addr", formatAddr(addr),
			"info_hash", hex.EncodeToString(infoHash[:]),
		)
		if d.metrics != nil {
			d.metrics.TokenFailure.Inc()
		}
		d.server.Reply(&krpc.Message{
			TransactionID: msg.TransactionID,
			Type:          "e",
			Error:         []any{203, "bad token"},
		}, addr)
		return
	}

	peerAddr := addr
	impliedPort, _ := msg.Args["implied_port"].(int)
	if impliedPort != 1 {
		port, ok := msg.Args["port"].(int)
		if !ok {
			return
		}
		peerAddr = netip.AddrPortFrom(addr.Addr(), uint16(port))
	}

	d.peers.Add(infoHash, peerAddr)

	if d.metrics != nil {
		d.metrics.TokenSuccess.Inc()
	}

	d.config.Logger.Debug("peer announced",
		"addr", formatAddr(addr),
		"info_hash", hex.EncodeToString(infoHash[:]),
		"peer_addr", peerAddr.String(),
	)

	resp := &krpc.Message{
		TransactionID: msg.TransactionID,
		Type:          "r",
		IP:            compactAddr(addr),
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

// --- BEP 42 helpers ---

// compactAddr encodes an AddrPort as 6 bytes (4 IP + 2 port) for the BEP 42 "ip" field.
func compactAddr(addr netip.AddrPort) []byte {
	ip4 := addr.Addr().As4()
	var buf [6]byte
	copy(buf[:4], ip4[:])
	binary.BigEndian.PutUint16(buf[4:6], addr.Port())
	return buf[:]
}

// parseCompactAddr decodes a 6-byte compact address into an AddrPort.
func parseCompactAddr(data []byte) (netip.AddrPort, bool) {
	if len(data) != 6 {
		return netip.AddrPort{}, false
	}
	ip := netip.AddrFrom4([4]byte(data[:4]))
	port := binary.BigEndian.Uint16(data[4:6])
	return netip.AddrPortFrom(ip, port), true
}

// voteExternalIP records an external IP vote from a bootstrap response.
// Returns the consensus IP once >= 3 nodes agree.
func (d *DHT) voteExternalIP(ip netip.Addr) (netip.Addr, bool) {
	d.ipVotesMu.Lock()
	defer d.ipVotesMu.Unlock()
	d.ipVotes[ip]++

	var bestIP netip.Addr
	bestCount := 0
	for addr, count := range d.ipVotes {
		if count > bestCount {
			bestIP = addr
			bestCount = count
		}
	}
	if bestCount >= 3 {
		return bestIP, true
	}
	return netip.Addr{}, false
}

// regenerateCompliantID generates a BEP 42 compliant node ID for the given
// external IP, rebuilds the routing table, and re-inserts existing nodes.
func (d *DHT) regenerateCompliantID(ip netip.Addr) {
	newID, err := hardening.GenerateCompliantID(ip)
	if err != nil {
		d.config.Logger.Warn("BEP 42: failed to generate compliant ID", "err", err)
		return
	}

	oldID := d.id
	nodes := d.table.Snapshot()

	d.id = newID
	d.externalIP = ip
	d.table = routing.NewRoutingTable(newID, d.config.Logger)

	// Re-insert existing nodes with BEP 42 validation.
	for _, n := range nodes {
		if d.config.BEP42 != BEP42Off {
			n.Compliant = hardening.ValidateNodeID(n.ID, n.Addr.Addr())
		}
		d.table.Insert(n)
	}

	d.config.Logger.Info("BEP 42: regenerated compliant node ID",
		"old_id", oldID.String(),
		"new_id", newID.String(),
		"external_ip", ip.String(),
		"reinserted_nodes", len(nodes),
	)
}

// formatAddr returns a clean address string, unmapping IPv4-mapped IPv6
// addresses (e.g., "::ffff:1.2.3.4:6881" → "1.2.3.4:6881").
func formatAddr(addr netip.AddrPort) string {
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()).String()
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
