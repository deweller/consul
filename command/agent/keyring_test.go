package agent

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/consul/testutil"
)

func TestAgent_LoadKeyrings(t *testing.T) {
	key := "tbLJg26ZJyJ9pK3qhc9jig=="

	// Should be no configured keyring file by default
	a1 := NewTestAgent(t, nextConfig())
	defer a1.Shutdown()

	cc := a1.ConsulConfig()
	if cc.SerfLANConfig.KeyringFile != "" {
		t.Fatalf("bad: %#v", cc.SerfLANConfig.KeyringFile)
	}
	if cc.SerfLANConfig.MemberlistConfig.Keyring != nil {
		t.Fatalf("keyring should not be loaded")
	}
	if cc.SerfWANConfig.KeyringFile != "" {
		t.Fatalf("bad: %#v", cc.SerfLANConfig.KeyringFile)
	}
	if cc.SerfWANConfig.MemberlistConfig.Keyring != nil {
		t.Fatalf("keyring should not be loaded")
	}

	// Server should auto-load LAN and WAN keyring files
	a2 := NewTestAgent(t, keyringConfig(key))
	defer a2.Shutdown()

	cc = a2.config.ConsulConfig
	if cc.SerfLANConfig.KeyringFile == "" {
		t.Fatalf("should have keyring file")
	}
	if cc.SerfLANConfig.MemberlistConfig.Keyring == nil {
		t.Fatalf("keyring should be loaded")
	}
	if cc.SerfWANConfig.KeyringFile == "" {
		t.Fatalf("should have keyring file")
	}
	if cc.SerfWANConfig.MemberlistConfig.Keyring == nil {
		t.Fatalf("keyring should be loaded")
	}

	// Client should auto-load only the LAN keyring file
	conf3 := keyringConfig(key)
	conf3.Server = false
	a3 := NewTestAgent(t, conf3)
	defer a3.Shutdown()

	cc = a3.config.ConsulConfig
	if cc.SerfLANConfig.KeyringFile == "" {
		t.Fatalf("should have keyring file")
	}
	if cc.SerfLANConfig.MemberlistConfig.Keyring == nil {
		t.Fatalf("keyring should be loaded")
	}
	if cc.SerfWANConfig.KeyringFile != "" {
		t.Fatalf("bad: %#v", cc.SerfWANConfig.KeyringFile)
	}
	if cc.SerfWANConfig.MemberlistConfig.Keyring != nil {
		t.Fatalf("keyring should not be loaded")
	}
}

func TestAgent_InitKeyring(t *testing.T) {
	key1 := "tbLJg26ZJyJ9pK3qhc9jig=="
	key2 := "4leC33rgtXKIVUr9Nr0snQ=="
	expected := fmt.Sprintf(`["%s"]`, key1)

	dir := testutil.TempDir(t, "consul")
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "keyring")

	// First initialize the keyring
	if err := initKeyring(file, key1); err != nil {
		t.Fatalf("err: %s", err)
	}

	content, err := ioutil.ReadFile(file)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if string(content) != expected {
		t.Fatalf("bad: %s", content)
	}

	// Try initializing again with a different key
	if err := initKeyring(file, key2); err != nil {
		t.Fatalf("err: %s", err)
	}

	// Content should still be the same
	content, err = ioutil.ReadFile(file)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if string(content) != expected {
		t.Fatalf("bad: %s", content)
	}
}

func TestAgentKeyring_ACL(t *testing.T) {
	key1 := "tbLJg26ZJyJ9pK3qhc9jig=="
	key2 := "4leC33rgtXKIVUr9Nr0snQ=="

	conf := keyringConfig(key1)
	conf.ACLDatacenter = "dc1"
	conf.ACLMasterToken = "root"
	conf.ACLDefaultPolicy = "deny"
	a := NewTestAgent(t, conf)
	defer a.Shutdown()

	// List keys without access fails
	_, err := a.ListKeys("", 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got: %#v", err)
	}

	// List keys with access works
	_, err = a.ListKeys("root", 0)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Install without access fails
	_, err = a.InstallKey(key2, "", 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got: %#v", err)
	}

	// Install with access works
	_, err = a.InstallKey(key2, "root", 0)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Use without access fails
	_, err = a.UseKey(key2, "", 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got: %#v", err)
	}

	// Use with access works
	_, err = a.UseKey(key2, "root", 0)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Remove without access fails
	_, err = a.RemoveKey(key1, "", 0)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got: %#v", err)
	}

	// Remove with access works
	_, err = a.RemoveKey(key1, "root", 0)
	if err != nil {
		t.Fatalf("err: %s", err)
	}
}
