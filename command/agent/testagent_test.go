package agent

import (
	"os"
	"testing"

	"github.com/hashicorp/consul/consul"
	"github.com/hashicorp/consul/consul/structs"
	"github.com/hashicorp/consul/testutil"
	"github.com/hashicorp/consul/testutil/retry"
)

type TestAgent struct {
	*Agent
	srv *HTTPServer
}

func NewUnstartedTestAgent(t *testing.T, c *Config) *TestAgent {
	if c.DataDir == "" {
		c.DataDir = testutil.TempDir(t, "agent")
	}
	a, err := NewAgent(c, nil, nil, nil)
	if err != nil {
		os.RemoveAll(c.DataDir)
		t.Fatalf("Error creating agent: %s", err)
	}
	return &TestAgent{a, nil}
}

func NewTestAgent(t *testing.T, c *Config) *TestAgent {
	a := NewUnstartedTestAgent(t, c)
	if err := a.Start(); err != nil {
		t.Fatal("Error starting agent: ", err)
	}

	var out structs.IndexedNodes
	retry.Run(t, func(r *retry.R) {
		if len(a.httpServers) == 0 || a.httpServers[0].srv == nil {
			r.Fatal("waiting for server")
		}
		// Ensure we have a leader and a node registration.
		args := &structs.DCSpecificRequest{Datacenter: a.config.Datacenter}
		if err := a.RPC("Catalog.ListNodes", args, &out); err != nil {
			r.Fatalf("Catalog.ListNodes failed: %v", err)
		}
		if !out.QueryMeta.KnownLeader {
			r.Fatalf("No leader")
		}
		if out.Index == 0 {
			r.Fatalf("Consul index is 0")
		}
	})

	// todo(fs): we should probably have a function for this
	a.srv = a.httpServers[0]
	return a
}

func (a *TestAgent) Shutdown() error {
	defer os.RemoveAll(a.config.DataDir)
	return a.Agent.Shutdown()
}

func (a *TestAgent) HTTP() *HTTPServer {
	if len(a.httpServers) > 0 {
		return a.httpServers[0]
	}
	return nil
}

func (a *TestAgent) ConsulConfig() *consul.Config {
	c, err := a.consulConfig()
	if err != nil {
		panic(err)
	}
	return c
}
