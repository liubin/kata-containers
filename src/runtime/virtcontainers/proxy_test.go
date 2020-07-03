// Copyright (c) 2017 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0
//

package virtcontainers

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/persist/fs"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

var testDefaultLogger = logrus.WithField("proxy", "test")

func TestNewProxy(t *testing.T) {
	expectedProxy := &kataBuiltInProxy{}
	result, err := newProxy()
	assert := assert.New(t)
	assert.NoError(err)
	assert.Exactly(result, expected)

}

func testNewProxyFromSandboxConfig(t *testing.T, sandboxConfig SandboxConfig) {
	assert := assert.New(t)

	_, err := newProxy()
	assert.NoError(err)

	err = validateProxyConfig(sandboxConfig.ProxyConfig)
	assert.NoError(err)
}

var testProxyPath = "proxy-path"

func TestNewProxyConfigFromKataProxySandboxConfig(t *testing.T) {
	proxyConfig := ProxyConfig{
		Debug: true,
	}

	sandboxConfig := SandboxConfig{
		ProxyConfig: proxyConfig,
	}

	testNewProxyFromSandboxConfig(t, sandboxConfig)
}

func TestNewProxyConfigNoPathFailure(t *testing.T) {
	assert.Error(t, validateProxyConfig(ProxyConfig{}))
}

const sandboxID = "123456789"

func testDefaultProxyURL(expectedURL string, socketType string, sandboxID string) error {
	sandbox := &Sandbox{
		id: sandboxID,
	}

	url, err := defaultProxyURL(sandbox.id, socketType)
	if err != nil {
		return err
	}

	if url != expectedURL {
		return fmt.Errorf("Mismatched URL: %s vs %s", url, expectedURL)
	}

	return nil
}

func TestDefaultProxyURLUnix(t *testing.T) {
	path := filepath.Join(filepath.Join(fs.MockRunStoragePath(), sandboxID), "proxy.sock")
	socketPath := fmt.Sprintf("unix://%s", path)
	assert.NoError(t, testDefaultProxyURL(socketPath, SocketTypeUNIX, sandboxID))
}

func TestDefaultProxyURLVSock(t *testing.T) {
	assert.NoError(t, testDefaultProxyURL("", SocketTypeVSOCK, sandboxID))
}

func TestDefaultProxyURLUnknown(t *testing.T) {
	path := filepath.Join(filepath.Join(fs.MockRunStoragePath(), sandboxID), "proxy.sock")
	socketPath := fmt.Sprintf("unix://%s", path)
	assert.Error(t, testDefaultProxyURL(socketPath, "foobar", sandboxID))
}

func testProxyStart(t *testing.T, agent agent, proxy proxy) {
	assert := assert.New(t)

	assert.NotNil(proxy)

	tmpdir, err := ioutil.TempDir("", "")
	assert.NoError(err)
	defer os.RemoveAll(tmpdir)

	type testData struct {
		params      proxyParams
		expectedURI string
		expectError bool
	}

	invalidPath := filepath.Join(tmpdir, "enoent")
	expectedSocketPath := filepath.Join(filepath.Join(fs.MockRunStoragePath(), testSandboxID), "proxy.sock")
	expectedURI := fmt.Sprintf("unix://%s", expectedSocketPath)

	data := []testData{
		{proxyParams{}, "", true},
		{
			// no path
			proxyParams{
				id:         "foobar",
				agentURL:   "agentURL",
				consoleURL: "consoleURL",
				logger:     testDefaultLogger,
			},
			"", true,
		},
		{
			// invalid path
			proxyParams{
				id:         "foobar",
				path:       invalidPath,
				agentURL:   "agentURL",
				consoleURL: "consoleURL",
				logger:     testDefaultLogger,
			},
			"", true,
		},
		{
			// good case
			proxyParams{
				id:         testSandboxID,
				path:       "echo",
				agentURL:   "agentURL",
				consoleURL: "consoleURL",
				logger:     testDefaultLogger,
			},
			expectedURI, false,
		},
	}

	for _, d := range data {
		pid, uri, err := proxy.start(d.params)
		if d.expectError {
			assert.Error(err)
			continue
		}

		assert.NoError(err)
		assert.True(pid > 0)
		assert.Equal(d.expectedURI, uri)
	}
}

func TestValidateProxyConfig(t *testing.T) {
	assert := assert.New(t)

	config := ProxyConfig{}
	err := validateProxyConfig(config)
	assert.Error(err)

	config.Path = "foobar"
	err = validateProxyConfig(config)
	assert.Nil(err)
}

func TestValidateProxyParams(t *testing.T) {
	assert := assert.New(t)

	p := proxyParams{}
	err := validateProxyParams(p)
	assert.Error(err)

	p.path = "foobar"
	err = validateProxyParams(p)
	assert.Error(err)

	p.id = "foobar1"
	err = validateProxyParams(p)
	assert.Error(err)

	p.agentURL = "foobar2"
	err = validateProxyParams(p)
	assert.Error(err)

	p.consoleURL = "foobar3"
	err = validateProxyParams(p)
	assert.Error(err)

	p.logger = &logrus.Entry{}
	err = validateProxyParams(p)
	assert.Nil(err)
}
