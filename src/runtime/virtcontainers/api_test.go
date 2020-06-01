// Copyright (c) 2016 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0
//

package virtcontainers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ktu "github.com/kata-containers/kata-containers/src/runtime/pkg/katatestutils"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/persist"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/annotations"
	vccgroups "github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/cgroups"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/mock"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/rootless"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
)

const (
	containerID = "1"
)

var sandboxAnnotations = map[string]string{
	"sandbox.foo":   "sandbox.bar",
	"sandbox.hello": "sandbox.world",
}

var containerAnnotations = map[string]string{
	"container.foo":   "container.bar",
	"container.hello": "container.world",
}

func init() {
	rootless.IsRootless = func() bool { return false }
}

func newEmptySpec() *specs.Spec {
	return &specs.Spec{
		Linux: &specs.Linux{
			Resources:   &specs.LinuxResources{},
			CgroupsPath: vccgroups.DefaultCgroupPath,
		},
		Process: &specs.Process{
			Capabilities: &specs.LinuxCapabilities{},
		},
	}
}

func newBasicTestCmd() types.Cmd {
	envs := []types.EnvVar{
		{
			Var:   "PATH",
			Value: "/bin:/usr/bin:/sbin:/usr/sbin",
		},
	}

	cmd := types.Cmd{
		Args:    strings.Split("/bin/sh", " "),
		Envs:    envs,
		WorkDir: "/",
	}

	return cmd
}

func rmSandboxDir(sid string) error {
	store, err := persist.GetDriver()
	if err != nil {
		return fmt.Errorf("failed to get fs persist driver: %v", err)
	}

	store.Destroy(sid)
	return nil
}

func newTestSandboxConfigNoop() SandboxConfig {
	bundlePath := filepath.Join(testDir, testBundle)
	containerAnnotations[annotations.BundlePathKey] = bundlePath
	// containerAnnotations["com.github.containers.virtcontainers.pkg.oci.container_type"] = "pod_sandbox"

	emptySpec := newEmptySpec()

	// Define the container command and bundle.
	container := ContainerConfig{
		ID:          containerID,
		RootFs:      RootFs{Target: bundlePath, Mounted: true},
		Cmd:         newBasicTestCmd(),
		Annotations: containerAnnotations,
		CustomSpec:  emptySpec,
	}

	// Sets the hypervisor configuration.
	hypervisorConfig := HypervisorConfig{
		KernelPath:     filepath.Join(testDir, testKernel),
		ImagePath:      filepath.Join(testDir, testImage),
		HypervisorPath: filepath.Join(testDir, testHypervisor),
	}

	sandboxConfig := SandboxConfig{
		ID:               testSandboxID,
		HypervisorType:   MockHypervisor,
		HypervisorConfig: hypervisorConfig,

		AgentType: NoopAgentType,

		Containers: []ContainerConfig{container},

		Annotations: sandboxAnnotations,

		ProxyType: NoopProxyType,
	}

	configFile := filepath.Join(bundlePath, "config.json")
	f, err := os.OpenFile(configFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return SandboxConfig{}
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(emptySpec); err != nil {
		return SandboxConfig{}
	}

	return sandboxConfig
}

func newTestSandboxConfigKataAgent() SandboxConfig {
	sandboxConfig := newTestSandboxConfigNoop()
	sandboxConfig.AgentType = KataContainersAgent
	sandboxConfig.AgentConfig = KataAgentConfig{}
	sandboxConfig.Containers = nil

	return sandboxConfig
}

func TestCreateSandboxNoopAgentSuccessful(t *testing.T) {
	defer cleanUp()
	assert := assert.New(t)

	config := newTestSandboxConfigNoop()

	p, err := CreateSandbox(context.Background(), config, nil)
	assert.NoError(err)
	assert.NotNil(p)

	s, ok := p.(*Sandbox)
	assert.True(ok)
	sandboxDir := filepath.Join(s.newStore.RunStoragePath(), p.ID())
	_, err = os.Stat(sandboxDir)
	assert.NoError(err)
}

func TestCreateSandboxKataAgentSuccessful(t *testing.T) {
	assert := assert.New(t)
	if tc.NotValid(ktu.NeedRoot()) {
		t.Skip(testDisabledAsNonRoot)
	}

	defer cleanUp()

	config := newTestSandboxConfigKataAgent()

	sockDir, err := testGenerateKataProxySockDir()
	assert.NoError(err)

	defer os.RemoveAll(sockDir)

	testKataProxyURL := fmt.Sprintf(testKataProxyURLTempl, sockDir)
	noopProxyURL = testKataProxyURL

	impl := &gRPCProxy{}

	kataProxyMock := mock.ProxyGRPCMock{
		GRPCImplementer: impl,
		GRPCRegister:    gRPCRegister,
	}
	err = kataProxyMock.Start(testKataProxyURL)
	assert.NoError(err)
	defer kataProxyMock.Stop()

	p, err := CreateSandbox(context.Background(), config, nil)
	assert.NoError(err)
	assert.NotNil(p)

	s, ok := p.(*Sandbox)
	assert.True(ok)
	sandboxDir := filepath.Join(s.newStore.RunStoragePath(), p.ID())
	_, err = os.Stat(sandboxDir)
	assert.NoError(err)
}

func TestCreateSandboxFailing(t *testing.T) {
	defer cleanUp()
	assert := assert.New(t)

	config := SandboxConfig{}

	p, err := CreateSandbox(context.Background(), config, nil)
	assert.Error(err)
	assert.Nil(p.(*Sandbox))
}

/*
 * Benchmarks
 */

func createNewSandboxConfig(hType HypervisorType, aType AgentType, aConfig interface{}) SandboxConfig {
	hypervisorConfig := HypervisorConfig{
		KernelPath:     "/usr/share/kata-containers/vmlinux.container",
		ImagePath:      "/usr/share/kata-containers/kata-containers.img",
		HypervisorPath: "/usr/bin/qemu-system-x86_64",
	}

	netConfig := NetworkConfig{}

	return SandboxConfig{
		ID:               testSandboxID,
		HypervisorType:   hType,
		HypervisorConfig: hypervisorConfig,

		AgentType:   aType,
		AgentConfig: aConfig,

		NetworkConfig: netConfig,

		ProxyType: NoopProxyType,
	}
}

func newTestContainerConfigNoop(contID string) ContainerConfig {
	// Define the container command and bundle.
	container := ContainerConfig{
		ID:          contID,
		RootFs:      RootFs{Target: filepath.Join(testDir, testBundle), Mounted: true},
		Cmd:         newBasicTestCmd(),
		Annotations: containerAnnotations,
		CustomSpec:  newEmptySpec(),
	}

	return container
}

// createAndStartSandbox handles the common test operation of creating and
// starting a sandbox.
func createAndStartSandbox(ctx context.Context, config SandboxConfig) (sandbox VCSandbox, sandboxDir string,
	err error) {

	// Create sandbox
	sandbox, err = CreateSandbox(ctx, config, nil)
	if sandbox == nil || err != nil {
		return nil, "", err
	}

	s, ok := sandbox.(*Sandbox)
	if !ok {
		return nil, "", fmt.Errorf("Could not get Sandbox")
	}
	sandboxDir = filepath.Join(s.newStore.RunStoragePath(), sandbox.ID())
	_, err = os.Stat(sandboxDir)
	if err != nil {
		return nil, "", err
	}

	// Start sandbox
	// sandbox, err = StartSandbox(ctx, sandbox.ID())
	// if sandbox == nil || err != nil {
	// 	return nil, "", err
	// }

	return sandbox, sandboxDir, nil
}

func TestReleaseSandbox(t *testing.T) {
	defer cleanUp()

	config := newTestSandboxConfigNoop()

	s, err := CreateSandbox(context.Background(), config, nil)
	assert.NoError(t, err)
	assert.NotNil(t, s)

	err = s.Release()
	assert.Nil(t, err, "sandbox release failed: %v", err)
}

func TestCleanupContainer(t *testing.T) {
	config := newTestSandboxConfigNoop()
	assert := assert.New(t)

	ctx := context.Background()

	p, _, err := createAndStartSandbox(ctx, config)
	if p == nil || err != nil {
		t.Fatal(err)
	}

	contIDs := []string{"100", "101", "102", "103", "104"}
	for _, contID := range contIDs {
		contConfig := newTestContainerConfigNoop(contID)

		c, err := p.CreateContainer(contConfig)
		if c == nil || err != nil {
			t.Fatal(err)
		}

		c, err = p.StartContainer(c.ID())
		if c == nil || err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range p.GetAllContainers() {
		CleanupContainer(ctx, p.ID(), c.ID(), true)
	}

	s, ok := p.(*Sandbox)
	assert.True(ok)
	sandboxDir := filepath.Join(s.newStore.RunStoragePath(), p.ID())

	_, err = os.Stat(sandboxDir)
	if err == nil {
		t.Fatal(err)
	}
}
