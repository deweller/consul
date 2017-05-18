package agent

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/coreos/etcd/version"
	"github.com/hashicorp/consul/consul"
	"github.com/hashicorp/consul/testutil"
	"github.com/hashicorp/consul/types"
	uuid "github.com/hashicorp/go-uuid"
)

const (
	basePortNumber = 10000

	portOffsetDNS = iota
	portOffsetHTTP
	portOffsetSerfLan
	portOffsetSerfWan
	portOffsetServer

	// Must be last in list
	numPortsPerIndex
)

var offset uint64 = basePortNumber

func nextConfig() *Config {
	idx := int(atomic.AddUint64(&offset, numPortsPerIndex))
	conf := DefaultConfig()

	nodeID, err := uuid.GenerateUUID()
	if err != nil {
		panic(err)
	}

	conf.Version = version.Version
	conf.VersionPrerelease = "c.d"
	conf.AdvertiseAddr = "127.0.0.1"
	conf.Bootstrap = true
	conf.Datacenter = "dc1"
	conf.NodeName = fmt.Sprintf("Node %d", idx)
	conf.NodeID = types.NodeID(nodeID)
	conf.BindAddr = "127.0.0.1"
	//	conf.Ports.DNS = basePortNumber + idx + portOffsetDNS
	conf.Ports.HTTP = basePortNumber + idx + portOffsetHTTP
	conf.Ports.SerfLan = basePortNumber + idx + portOffsetSerfLan
	conf.Ports.SerfWan = basePortNumber + idx + portOffsetSerfWan
	conf.Ports.Server = basePortNumber + idx + portOffsetServer
	conf.Server = true
	conf.ACLEnforceVersion8 = Bool(false)
	conf.ACLDatacenter = "dc1"
	conf.ACLMasterToken = "root"

	cons := consul.DefaultConfig()
	conf.ConsulConfig = cons

	cons.SerfLANConfig.MemberlistConfig.SuspicionMult = 3
	cons.SerfLANConfig.MemberlistConfig.ProbeTimeout = 100 * time.Millisecond
	cons.SerfLANConfig.MemberlistConfig.ProbeInterval = 100 * time.Millisecond
	cons.SerfLANConfig.MemberlistConfig.GossipInterval = 100 * time.Millisecond

	cons.SerfWANConfig.MemberlistConfig.SuspicionMult = 3
	cons.SerfWANConfig.MemberlistConfig.ProbeTimeout = 100 * time.Millisecond
	cons.SerfWANConfig.MemberlistConfig.ProbeInterval = 100 * time.Millisecond
	cons.SerfWANConfig.MemberlistConfig.GossipInterval = 100 * time.Millisecond

	cons.RaftConfig.LeaderLeaseTimeout = 20 * time.Millisecond
	cons.RaftConfig.HeartbeatTimeout = 40 * time.Millisecond
	cons.RaftConfig.ElectionTimeout = 40 * time.Millisecond

	cons.CoordinateUpdatePeriod = 100 * time.Millisecond
	cons.ServerHealthInterval = 10 * time.Millisecond

	return conf
}

func nextACLConfig() *Config {
	c := nextConfig()
	c.ACLDatacenter = c.Datacenter
	c.ACLDefaultPolicy = "deny"
	c.ACLMasterToken = "root"
	c.ACLAgentToken = "root"
	c.ACLAgentMasterToken = "towel"
	c.ACLEnforceVersion8 = Bool(true)
	return c
}

func keyringConfig(key string) *Config {
	c := nextConfig()
	c.DataDir = testutil.TempDir(nil, "agent")

	fileLAN := filepath.Join(c.DataDir, serfLANKeyring)
	if err := initKeyring(fileLAN, key); err != nil {
		panic(fmt.Sprintf("cannot create LAN keyring: %s", err))
	}
	fileWAN := filepath.Join(c.DataDir, serfWANKeyring)
	if err := initKeyring(fileWAN, key); err != nil {
		panic(fmt.Sprintf("cannot create WAN keyring: %s", err))
	}
	return c
}

//func makeAgentLog(t *testing.T, conf *Config, l io.Writer, writer *logger.LogWriter) (string, *Agent) {
//	dir := testutil.TempDir(t, "agent")
//
//	conf.DataDir = dir
//	agent, err := Create(conf, l, writer, nil)
//	if err != nil {
//		os.RemoveAll(dir)
//		t.Fatalf(fmt.Sprintf("err: %v", err))
//	}
//
//	return dir, agent
//}
//
//func makeAgentKeyring(t *testing.T, conf *Config, key string) (string, *Agent) {
//	dir := testutil.TempDir(t, "agent")
//
//	conf.DataDir = dir
//
//	fileLAN := filepath.Join(dir, serfLANKeyring)
//	if err := initKeyring(fileLAN, key); err != nil {
//		t.Fatalf("err: %s", err)
//	}
//	fileWAN := filepath.Join(dir, serfWANKeyring)
//	if err := initKeyring(fileWAN, key); err != nil {
//		t.Fatalf("err: %s", err)
//	}
//
//	agent, err := Create(conf, nil, nil, nil)
//	if err != nil {
//		t.Fatalf("err: %s", err)
//	}
//
//	return dir, agent
//}
//
//func makeAgent(t *testing.T, conf *Config) (string, *Agent) {
//	return makeAgentLog(t, conf, nil, nil)
//}
//
