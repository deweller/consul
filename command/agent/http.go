package agent

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/armon/go-metrics"
	"github.com/hashicorp/consul/consul/structs"
	"github.com/hashicorp/consul/tlsutil"
	"github.com/mitchellh/mapstructure"
)

// HTTPServer is used to wrap an Agent and expose various API's
// in a RESTful manner
type HTTPServer struct {
	agent  *Agent
	logger *log.Logger
	srv    *http.Server
}

func NewHTTPServer(a *Agent) *HTTPServer {
	return &HTTPServer{agent: a, logger: a.logger}
}

func (s *HTTPServer) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

func (s *HTTPServer) ListenAndServeTLS(addr string, cfg *tls.Config) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	l = tls.NewListener(tcpKeepAliveListener{l.(*net.TCPListener)}, cfg)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

func (s *HTTPServer) ListenAndServeUnix(addr string, perm FilePermissions) error {
	if _, err := os.Stat(addr); !os.IsNotExist(err) {
		s.agent.logger.Printf("[WARN] agent: Replacing socket %q", addr)
	}
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error removing socket file: %s", err)
	}
	l, err := net.Listen("unix", addr)
	if err != nil {
		return err
	}
	if err := setFilePermissions(addr, perm); err != nil {
		return fmt.Errorf("Failed setting up HTTP socket: %s", err)
	}
	return s.Serve(l)
}

func (s *HTTPServer) Serve(l net.Listener) error {
	s.srv = &http.Server{
		Addr:    l.Addr().String(),
		Handler: s.handler(s.agent.config.EnableDebug),
	}
	return s.srv.Serve(l)
}

// Shutdown stops the server and closes all open connections.
func (s *HTTPServer) Shutdown() {
	s.logger.Printf("[DEBUG] http: Shutting down http server (%v)", s.srv.Addr)

	// todo(fs): Close() stops the server immediately.
	// This models the previous behavior of closing the listener.
	s.srv.Close()

	// todo(fs): Shutdown() will stop the server gracefully within
	// the given timeout. This prolongs some tests but might be the
	// better choice.
	// ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	// defer cancel()
	// s.srv.Shutdown(ctx)
}

// handler is used to attach our handlers to the mux
func (s *HTTPServer) handler(enableDebug bool) http.Handler {
	mux := http.NewServeMux()

	// handleFuncMetrics takes the given pattern and handler and wraps to produce
	// metrics based on the pattern and request.
	handleFuncMetrics := func(pattern string, handler http.HandlerFunc) {
		// Get the parts of the pattern. We omit any initial empty for the
		// leading slash, and put an underscore as a "thing" placeholder if we
		// see a trailing slash, which means the part after is parsed. This lets
		// us distinguish from things like /v1/query and /v1/query/<query id>.
		var parts []string
		for i, part := range strings.Split(pattern, "/") {
			if part == "" {
				if i == 0 {
					continue
				}
				part = "_"
			}
			parts = append(parts, part)
		}

		// Register the wrapper, which will close over the expensive-to-compute
		// parts from above.
		wrapper := func(resp http.ResponseWriter, req *http.Request) {
			start := time.Now()
			handler(resp, req)
			key := append([]string{"consul", "http", req.Method}, parts...)
			metrics.MeasureSince(key, start)
		}
		mux.HandleFunc(pattern, wrapper)
	}

	mux.HandleFunc("/", s.Index)

	// API V1.
	if s.agent.config.ACLDatacenter != "" {
		handleFuncMetrics("/v1/acl/create", s.wrap(s.ACLCreate))
		handleFuncMetrics("/v1/acl/update", s.wrap(s.ACLUpdate))
		handleFuncMetrics("/v1/acl/destroy/", s.wrap(s.ACLDestroy))
		handleFuncMetrics("/v1/acl/info/", s.wrap(s.ACLGet))
		handleFuncMetrics("/v1/acl/clone/", s.wrap(s.ACLClone))
		handleFuncMetrics("/v1/acl/list", s.wrap(s.ACLList))
		handleFuncMetrics("/v1/acl/replication", s.wrap(s.ACLReplicationStatus))
	} else {
		handleFuncMetrics("/v1/acl/create", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/update", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/destroy/", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/info/", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/clone/", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/list", s.wrap(ACLDisabled))
		handleFuncMetrics("/v1/acl/replication", s.wrap(ACLDisabled))
	}
	handleFuncMetrics("/v1/agent/self", s.wrap(s.AgentSelf))
	handleFuncMetrics("/v1/agent/maintenance", s.wrap(s.AgentNodeMaintenance))
	handleFuncMetrics("/v1/agent/reload", s.wrap(s.AgentReload))
	handleFuncMetrics("/v1/agent/monitor", s.wrap(s.AgentMonitor))
	handleFuncMetrics("/v1/agent/services", s.wrap(s.AgentServices))
	handleFuncMetrics("/v1/agent/checks", s.wrap(s.AgentChecks))
	handleFuncMetrics("/v1/agent/members", s.wrap(s.AgentMembers))
	handleFuncMetrics("/v1/agent/join/", s.wrap(s.AgentJoin))
	handleFuncMetrics("/v1/agent/leave", s.wrap(s.AgentLeave))
	handleFuncMetrics("/v1/agent/force-leave/", s.wrap(s.AgentForceLeave))
	handleFuncMetrics("/v1/agent/check/register", s.wrap(s.AgentRegisterCheck))
	handleFuncMetrics("/v1/agent/check/deregister/", s.wrap(s.AgentDeregisterCheck))
	handleFuncMetrics("/v1/agent/check/pass/", s.wrap(s.AgentCheckPass))
	handleFuncMetrics("/v1/agent/check/warn/", s.wrap(s.AgentCheckWarn))
	handleFuncMetrics("/v1/agent/check/fail/", s.wrap(s.AgentCheckFail))
	handleFuncMetrics("/v1/agent/check/update/", s.wrap(s.AgentCheckUpdate))
	handleFuncMetrics("/v1/agent/service/register", s.wrap(s.AgentRegisterService))
	handleFuncMetrics("/v1/agent/service/deregister/", s.wrap(s.AgentDeregisterService))
	handleFuncMetrics("/v1/agent/service/maintenance/", s.wrap(s.AgentServiceMaintenance))
	handleFuncMetrics("/v1/catalog/register", s.wrap(s.CatalogRegister))
	handleFuncMetrics("/v1/catalog/deregister", s.wrap(s.CatalogDeregister))
	handleFuncMetrics("/v1/catalog/datacenters", s.wrap(s.CatalogDatacenters))
	handleFuncMetrics("/v1/catalog/nodes", s.wrap(s.CatalogNodes))
	handleFuncMetrics("/v1/catalog/services", s.wrap(s.CatalogServices))
	handleFuncMetrics("/v1/catalog/service/", s.wrap(s.CatalogServiceNodes))
	handleFuncMetrics("/v1/catalog/node/", s.wrap(s.CatalogNodeServices))
	if !s.agent.config.DisableCoordinates {
		handleFuncMetrics("/v1/coordinate/datacenters", s.wrap(s.CoordinateDatacenters))
		handleFuncMetrics("/v1/coordinate/nodes", s.wrap(s.CoordinateNodes))
	} else {
		handleFuncMetrics("/v1/coordinate/datacenters", s.wrap(coordinateDisabled))
		handleFuncMetrics("/v1/coordinate/nodes", s.wrap(coordinateDisabled))
	}
	handleFuncMetrics("/v1/event/fire/", s.wrap(s.EventFire))
	handleFuncMetrics("/v1/event/list", s.wrap(s.EventList))
	handleFuncMetrics("/v1/health/node/", s.wrap(s.HealthNodeChecks))
	handleFuncMetrics("/v1/health/checks/", s.wrap(s.HealthServiceChecks))
	handleFuncMetrics("/v1/health/state/", s.wrap(s.HealthChecksInState))
	handleFuncMetrics("/v1/health/service/", s.wrap(s.HealthServiceNodes))
	handleFuncMetrics("/v1/internal/ui/nodes", s.wrap(s.UINodes))
	handleFuncMetrics("/v1/internal/ui/node/", s.wrap(s.UINodeInfo))
	handleFuncMetrics("/v1/internal/ui/services", s.wrap(s.UIServices))
	handleFuncMetrics("/v1/kv/", s.wrap(s.KVSEndpoint))
	handleFuncMetrics("/v1/operator/raft/configuration", s.wrap(s.OperatorRaftConfiguration))
	handleFuncMetrics("/v1/operator/raft/peer", s.wrap(s.OperatorRaftPeer))
	handleFuncMetrics("/v1/operator/keyring", s.wrap(s.OperatorKeyringEndpoint))
	handleFuncMetrics("/v1/operator/autopilot/configuration", s.wrap(s.OperatorAutopilotConfiguration))
	handleFuncMetrics("/v1/operator/autopilot/health", s.wrap(s.OperatorServerHealth))
	handleFuncMetrics("/v1/query", s.wrap(s.PreparedQueryGeneral))
	handleFuncMetrics("/v1/query/", s.wrap(s.PreparedQuerySpecific))
	handleFuncMetrics("/v1/session/create", s.wrap(s.SessionCreate))
	handleFuncMetrics("/v1/session/destroy/", s.wrap(s.SessionDestroy))
	handleFuncMetrics("/v1/session/renew/", s.wrap(s.SessionRenew))
	handleFuncMetrics("/v1/session/info/", s.wrap(s.SessionGet))
	handleFuncMetrics("/v1/session/node/", s.wrap(s.SessionsForNode))
	handleFuncMetrics("/v1/session/list", s.wrap(s.SessionList))
	handleFuncMetrics("/v1/status/leader", s.wrap(s.StatusLeader))
	handleFuncMetrics("/v1/status/peers", s.wrap(s.StatusPeers))
	handleFuncMetrics("/v1/snapshot", s.wrap(s.Snapshot))
	handleFuncMetrics("/v1/txn", s.wrap(s.Txn))

	// Debug endpoints.
	if enableDebug {
		handleFuncMetrics("/debug/pprof/", pprof.Index)
		handleFuncMetrics("/debug/pprof/cmdline", pprof.Cmdline)
		handleFuncMetrics("/debug/pprof/profile", pprof.Profile)
		handleFuncMetrics("/debug/pprof/symbol", pprof.Symbol)
	}

	// Use the custom UI dir if provided.
	if s.agent.config.UIDir != "" {
		mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.Dir(s.agent.config.UIDir))))
	} else if s.agent.config.EnableUI {
		mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(assetFS())))
	}
	return mux
}

// wrap is used to wrap functions to make them more convenient
func (s *HTTPServer) wrap(handler func(resp http.ResponseWriter, req *http.Request) (interface{}, error)) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		setHeaders(resp, s.agent.config.HTTPAPIResponseHeaders)
		setTranslateAddr(resp, s.agent.config.TranslateWanAddrs)

		// Obfuscate any tokens from appearing in the logs
		formVals, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			s.logger.Printf("[ERR] http: Failed to decode query: %s from=%s", err, req.RemoteAddr)
			resp.WriteHeader(http.StatusInternalServerError) // 500
			return
		}
		logURL := req.URL.String()
		if tokens, ok := formVals["token"]; ok {
			for _, token := range tokens {
				if token == "" {
					logURL += "<hidden>"
					continue
				}
				logURL = strings.Replace(logURL, token, "<hidden>", -1)
			}
		}

		handleErr := func(err error) {
			s.logger.Printf("[ERR] http: Request %s %v, error: %v from=%s", req.Method, logURL, err, req.RemoteAddr)
			code := http.StatusInternalServerError // 500
			errMsg := err.Error()
			if strings.Contains(errMsg, "Permission denied") || strings.Contains(errMsg, "ACL not found") {
				code = http.StatusForbidden // 403
			}
			resp.WriteHeader(code)
			fmt.Fprint(resp, errMsg)
		}

		// TODO (slackpad) We may want to consider redacting prepared
		// query names/IDs here since they are proxies for tokens. But,
		// knowing one only gives you read access to service listings
		// which is pretty trivial, so it's probably not worth the code
		// complexity and overhead of filtering them out. You can't
		// recover the token it's a proxy for with just the query info;
		// you'd need the actual token (or a management token) to read
		// that back.

		// Invoke the handler
		start := time.Now()
		defer func() {
			s.logger.Printf("[DEBUG] http: Request %s %v (%v) from=%s", req.Method, logURL, time.Now().Sub(start), req.RemoteAddr)
		}()
		obj, err := handler(resp, req)
		if err != nil {
			handleErr(err)
			return
		}
		if obj == nil {
			return
		}

		buf, err := s.marshalJSON(req, obj)
		if err != nil {
			handleErr(err)
			return
		}
		resp.Header().Set("Content-Type", "application/json")
		resp.Write(buf)
	}
}

// marshalJSON marshals the object into JSON, respecting the user's pretty-ness
// configuration.
func (s *HTTPServer) marshalJSON(req *http.Request, obj interface{}) ([]byte, error) {
	if _, ok := req.URL.Query()["pretty"]; ok || s.agent.config.DevMode {
		buf, err := json.MarshalIndent(obj, "", "    ")
		if err != nil {
			return nil, err
		}
		buf = append(buf, "\n"...)
		return buf, nil
	}

	buf, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return buf, err
}

// Returns true if the UI is enabled.
func (s *HTTPServer) IsUIEnabled() bool {
	return s.agent.config.UIDir != "" || s.agent.config.EnableUI
}

// Renders a simple index page
func (s *HTTPServer) Index(resp http.ResponseWriter, req *http.Request) {
	// Check if this is a non-index path
	if req.URL.Path != "/" {
		resp.WriteHeader(http.StatusNotFound) // 404
		return
	}

	// Give them something helpful if there's no UI so they at least know
	// what this server is.
	if !s.IsUIEnabled() {
		fmt.Fprint(resp, "Consul Agent")
		return
	}

	// Redirect to the UI endpoint
	http.Redirect(resp, req, "/ui/", http.StatusMovedPermanently) // 301
}

// decodeBody is used to decode a JSON request body
func decodeBody(req *http.Request, out interface{}, cb func(interface{}) error) error {
	var raw interface{}
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(&raw); err != nil {
		return err
	}

	// Invoke the callback prior to decode
	if cb != nil {
		if err := cb(raw); err != nil {
			return err
		}
	}
	return mapstructure.Decode(raw, out)
}

// setTranslateAddr is used to set the address translation header. This is only
// present if the feature is active.
func setTranslateAddr(resp http.ResponseWriter, active bool) {
	if active {
		resp.Header().Set("X-Consul-Translate-Addresses", "true")
	}
}

// setIndex is used to set the index response header
func setIndex(resp http.ResponseWriter, index uint64) {
	resp.Header().Set("X-Consul-Index", strconv.FormatUint(index, 10))
}

// setKnownLeader is used to set the known leader header
func setKnownLeader(resp http.ResponseWriter, known bool) {
	s := "true"
	if !known {
		s = "false"
	}
	resp.Header().Set("X-Consul-KnownLeader", s)
}

// setLastContact is used to set the last contact header
func setLastContact(resp http.ResponseWriter, last time.Duration) {
	lastMsec := uint64(last / time.Millisecond)
	resp.Header().Set("X-Consul-LastContact", strconv.FormatUint(lastMsec, 10))
}

// setMeta is used to set the query response meta data
func setMeta(resp http.ResponseWriter, m *structs.QueryMeta) {
	setIndex(resp, m.Index)
	setLastContact(resp, m.LastContact)
	setKnownLeader(resp, m.KnownLeader)
}

// setHeaders is used to set canonical response header fields
func setHeaders(resp http.ResponseWriter, headers map[string]string) {
	for field, value := range headers {
		resp.Header().Set(http.CanonicalHeaderKey(field), value)
	}
}

// parseWait is used to parse the ?wait and ?index query params
// Returns true on error
func parseWait(resp http.ResponseWriter, req *http.Request, b *structs.QueryOptions) bool {
	query := req.URL.Query()
	if wait := query.Get("wait"); wait != "" {
		dur, err := time.ParseDuration(wait)
		if err != nil {
			resp.WriteHeader(http.StatusBadRequest) // 400
			fmt.Fprint(resp, "Invalid wait time")
			return true
		}
		b.MaxQueryTime = dur
	}
	if idx := query.Get("index"); idx != "" {
		index, err := strconv.ParseUint(idx, 10, 64)
		if err != nil {
			resp.WriteHeader(http.StatusBadRequest) // 400
			fmt.Fprint(resp, "Invalid index")
			return true
		}
		b.MinQueryIndex = index
	}
	return false
}

// parseConsistency is used to parse the ?stale and ?consistent query params.
// Returns true on error
func parseConsistency(resp http.ResponseWriter, req *http.Request, b *structs.QueryOptions) bool {
	query := req.URL.Query()
	if _, ok := query["stale"]; ok {
		b.AllowStale = true
	}
	if _, ok := query["consistent"]; ok {
		b.RequireConsistent = true
	}
	if b.AllowStale && b.RequireConsistent {
		resp.WriteHeader(http.StatusBadRequest) // 400
		fmt.Fprint(resp, "Cannot specify ?stale with ?consistent, conflicting semantics.")
		return true
	}
	return false
}

// parseDC is used to parse the ?dc query param
func (s *HTTPServer) parseDC(req *http.Request, dc *string) {
	if other := req.URL.Query().Get("dc"); other != "" {
		*dc = other
	} else if *dc == "" {
		*dc = s.agent.config.Datacenter
	}
}

// parseToken is used to parse the ?token query param or the X-Consul-Token header
func (s *HTTPServer) parseToken(req *http.Request, token *string) {
	if other := req.URL.Query().Get("token"); other != "" {
		*token = other
		return
	}

	if other := req.Header.Get("X-Consul-Token"); other != "" {
		*token = other
		return
	}

	// Set the default ACLToken
	*token = s.agent.config.ACLToken
}

// parseSource is used to parse the ?near=<node> query parameter, used for
// sorting by RTT based on a source node. We set the source's DC to the target
// DC in the request, if given, or else the agent's DC.
func (s *HTTPServer) parseSource(req *http.Request, source *structs.QuerySource) {
	s.parseDC(req, &source.Datacenter)
	if node := req.URL.Query().Get("near"); node != "" {
		if node == "_agent" {
			source.Node = s.agent.config.NodeName
		} else {
			source.Node = node
		}
	}
}

// parseMetaFilter is used to parse the ?node-meta=key:value query parameter, used for
// filtering results to nodes with the given metadata key/value
func (s *HTTPServer) parseMetaFilter(req *http.Request) map[string]string {
	if filterList, ok := req.URL.Query()["node-meta"]; ok {
		filters := make(map[string]string)
		for _, filter := range filterList {
			key, value := parseMetaPair(filter)
			filters[key] = value
		}
		return filters
	}
	return nil
}

// parse is a convenience method for endpoints that need
// to use both parseWait and parseDC.
func (s *HTTPServer) parse(resp http.ResponseWriter, req *http.Request, dc *string, b *structs.QueryOptions) bool {
	s.parseDC(req, dc)
	s.parseToken(req, &b.Token)
	if parseConsistency(resp, req, b) {
		return true
	}
	return parseWait(resp, req, b)
}

func tlsConfig(config *Config) (*tls.Config, error) {
	tc := &tlsutil.Config{
		VerifyIncoming:           config.VerifyIncoming || config.VerifyIncomingHTTPS,
		VerifyOutgoing:           config.VerifyOutgoing,
		CAFile:                   config.CAFile,
		CAPath:                   config.CAPath,
		CertFile:                 config.CertFile,
		KeyFile:                  config.KeyFile,
		NodeName:                 config.NodeName,
		ServerName:               config.ServerName,
		TLSMinVersion:            config.TLSMinVersion,
		CipherSuites:             config.TLSCipherSuites,
		PreferServerCipherSuites: config.TLSPreferServerCipherSuites,
	}
	return tc.IncomingTLSConfig()
}

// tcpKeepAliveListener sets TCP keep-alive timeouts on accepted
// connections. It's used by NewHttpServer so
// dead TCP connections eventually go away.
type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(30 * time.Second)
	return tc, nil
}
