package command

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/command/agent"
	"github.com/hashicorp/consul/consul"
	"github.com/hashicorp/consul/logger"
	"github.com/hashicorp/consul/testutil"
	"github.com/hashicorp/consul/types"
	"github.com/hashicorp/consul/version"
	"github.com/hashicorp/go-uuid"
	"github.com/mitchellh/cli"
)

var offset uint64

func init() {
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	version.Version = "0.8.0"
}

type server struct {
	agent    *agent.Agent
	config   *agent.Config
	http     *agent.HTTPServer
	httpAddr string
	dir      string
	wg       sync.WaitGroup
}

func (s *server) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.http.ListenAndServe(s.httpAddr); err != nil {
			log.Print(err)
			// a.agent.logger.Print(err)
		}
	}()
}

func (a *server) Shutdown() {
	a.agent.Shutdown()
	a.http.Shutdown()
	a.wg.Wait()
	os.RemoveAll(a.dir)
}

func testAgent(t *testing.T) *server {
	return testAgentWithConfig(t, nil)
}

func testAgentWithAPIClient(t *testing.T) (*server, *api.Client) {
	agent := testAgentWithConfig(t, func(c *agent.Config) {})
	client, err := api.NewClient(&api.Config{Address: agent.httpAddr})
	if err != nil {
		t.Fatalf("consul client: %#v", err)
	}
	return agent, client
}

func testAgentWithConfig(t *testing.T, cb func(c *agent.Config)) *server {
	return testAgentWithConfigReload(t, cb, nil)
}

func testAgentWithConfigReload(t *testing.T, cb func(c *agent.Config), reloadCh chan chan error) *server {
	conf := nextConfig()
	if cb != nil {
		cb(conf)
	}

	conf.DataDir = testutil.TempDir(t, "agent")
	a, err := agent.Create(conf, logger.NewLogWriter(512), nil, reloadCh)
	if err != nil {
		os.RemoveAll(conf.DataDir)
		t.Fatalf("err: %v", err)
	}

	conf.Addresses.HTTP = "127.0.0.1"
	addr := fmt.Sprintf("%s:%d", conf.Addresses.HTTP, conf.Ports.HTTP)
	w := &server{
		agent:    a,
		config:   conf,
		http:     agent.NewHTTPServer(a),
		httpAddr: addr,
		dir:      conf.DataDir,
	}
	w.Start()
	return w
}

func nextConfig() *agent.Config {
	idx := int(atomic.AddUint64(&offset, 1))
	conf := agent.DefaultConfig()

	nodeID, err := uuid.GenerateUUID()
	if err != nil {
		panic(err)
	}

	conf.Bootstrap = true
	conf.Datacenter = "dc1"
	conf.NodeName = fmt.Sprintf("Node %d", idx)
	conf.NodeID = types.NodeID(nodeID)
	conf.BindAddr = "127.0.0.1"
	conf.Server = true

	conf.Version = version.Version

	conf.Ports.HTTP = 10000 + 10*idx
	conf.Ports.HTTPS = 10401 + 10*idx
	conf.Ports.SerfLan = 10201 + 10*idx
	conf.Ports.SerfWan = 10202 + 10*idx
	conf.Ports.Server = 10300 + 10*idx

	cons := consul.DefaultConfig()
	conf.ConsulConfig = cons

	cons.SerfLANConfig.MemberlistConfig.ProbeTimeout = 100 * time.Millisecond
	cons.SerfLANConfig.MemberlistConfig.ProbeInterval = 100 * time.Millisecond
	cons.SerfLANConfig.MemberlistConfig.GossipInterval = 100 * time.Millisecond

	cons.SerfWANConfig.MemberlistConfig.ProbeTimeout = 100 * time.Millisecond
	cons.SerfWANConfig.MemberlistConfig.ProbeInterval = 100 * time.Millisecond
	cons.SerfWANConfig.MemberlistConfig.GossipInterval = 100 * time.Millisecond

	cons.RaftConfig.LeaderLeaseTimeout = 20 * time.Millisecond
	cons.RaftConfig.HeartbeatTimeout = 40 * time.Millisecond
	cons.RaftConfig.ElectionTimeout = 40 * time.Millisecond

	return conf
}

func assertNoTabs(t *testing.T, c cli.Command) {
	if strings.ContainsRune(c.Help(), '\t') {
		t.Errorf("%#v help output contains tabs", c)
	}
}
