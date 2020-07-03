// Copyright (c) 2017 Intel Corporation
//
// SPDX-License-Identifier: Apache-2.0
//

package virtcontainers

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"

	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/persist"
	kataclient "github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/agent/protocols/client"
	"github.com/sirupsen/logrus"
)

var buildinProxyConsoleProto = consoleProtoUnix

type proxyBuiltin struct {
	sandboxID string
	conn      net.Conn
}

// ProxyConfig is a structure storing information needed from any
// proxy in order to be properly initialized.
type ProxyConfig struct {
	Debug bool
}

// proxyParams is the structure providing specific parameters needed
// for the execution of the proxy binary.
type proxyParams struct {
	id         string
	agentURL   string
	consoleURL string
	logger     *logrus.Entry
	hid        int
	debug      bool
}

const (
	// unix socket type of console
	consoleProtoUnix = "unix"

	// pty type of console. Used mostly by kvmtools.
	consoleProtoPty = "pty"
)

// newProxy returns a proxy from a proxy type.
func newProxy() (proxy, error) {
	return &proxyBuiltin{}, nil
}

func validateProxyParams(p proxyParams) error {
	if len(p.id) == 0 || len(p.agentURL) == 0 || len(p.consoleURL) == 0 {
		return fmt.Errorf("Invalid proxy parameters %+v", p)
	}

	if p.logger == nil {
		return fmt.Errorf("Invalid proxy parameter: proxy logger is not set")
	}

	return nil
}

func defaultProxyURL(id, socketType string) (string, error) {
	switch socketType {
	case SocketTypeUNIX:
		store, err := persist.GetDriver()
		if err != nil {
			return "", err
		}
		socketPath := filepath.Join(filepath.Join(store.RunStoragePath(), id), "proxy.sock")
		return fmt.Sprintf("unix://%s", socketPath), nil
	case SocketTypeVSOCK:
		// TODO Build the VSOCK default URL
		return "", nil
	default:
		return "", fmt.Errorf("Unknown socket type: %s", socketType)
	}
}

// proxy is the virtcontainers proxy interface.
type proxy interface {
	// start launches a proxy instance with specified parameters, returning
	// the PID of the process and the URL used to connect to it.
	start(params proxyParams) (int, string, error)

	// stop terminates a proxy instance after all communications with the
	// agent inside the VM have been properly stopped.
	stop(pid int) error

	//check if the proxy has watched the vm console.
	consoleWatched() bool
}

func (p *proxyBuiltin) watchConsole(proto, console string, logger *logrus.Entry) (err error) {
	var (
		scanner *bufio.Scanner
		conn    net.Conn
	)

	switch proto {
	case consoleProtoUnix:
		conn, err = net.Dial("unix", console)
		if err != nil {
			return err
		}
		// TODO: please see
		// https://github.com/kata-containers/kata-containers/src/runtime/issues/1940.
	case consoleProtoPty:
		fallthrough
	default:
		return fmt.Errorf("unknown console proto %s", proto)
	}

	p.conn = conn

	go func() {
		scanner = bufio.NewScanner(conn)
		for scanner.Scan() {
			logger.WithFields(logrus.Fields{
				"sandbox":   p.sandboxID,
				"vmconsole": scanner.Text(),
			}).Debug("reading guest console")
		}

		if err := scanner.Err(); err != nil {
			if err == io.EOF {
				logger.Info("console watcher quits")
			} else {
				logger.WithError(err).WithFields(logrus.Fields{
					"console-protocol": proto,
					"console-socket":   console,
				}).Error("Failed to read agent logs")
			}
		}
	}()

	return nil
}

// check if the proxy has watched the vm console.
func (p *proxyBuiltin) consoleWatched() bool {
	return p.conn != nil
}

// start is the proxy start implementation for builtin proxy.
// It starts the console watcher for the guest.
// It returns agentURL to let agent connect directly.
func (p *proxyBuiltin) start(params proxyParams) (int, string, error) {
	if err := p.validateParams(params); err != nil {
		return -1, "", err
	}
	if p.consoleWatched() {
		return -1, "", fmt.Errorf("The console has been watched for sandbox %s", params.id)
	}

	params.logger.Debug("Start to watch the console")

	p.sandboxID = params.id

	// For firecracker, it hasn't support the console watching and it's consoleURL
	// will be set empty.
	// TODO: add support for hybrid vsocks, see https://github.com/kata-containers/kata-containers/src/runtime/issues/2098
	if params.debug && params.consoleURL != "" && !strings.HasPrefix(params.consoleURL, kataclient.HybridVSockScheme) {
		err := p.watchConsole(buildinProxyConsoleProto, params.consoleURL, params.logger)
		if err != nil {
			p.sandboxID = ""
			return -1, "", err
		}
	}

	return params.hid, params.agentURL, nil
}

// stop is the proxy stop implementation for builtin proxy.
func (p *proxyBuiltin) stop(pid int) error {
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
		p.sandboxID = ""
	}
	return nil
}

func (p *proxyBuiltin) validateParams(params proxyParams) error {
	if len(params.id) == 0 || len(params.agentURL) == 0 || len(params.consoleURL) == 0 {
		return fmt.Errorf("Invalid proxy parameters %+v", params)
	}
	if params.logger == nil {
		return fmt.Errorf("Invalid proxy parameter: proxy logger is not set")
	}
	return nil
}
