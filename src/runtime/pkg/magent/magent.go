// Copyright (c) 2020 Ant Financial
//
// SPDX-License-Identifier: Apache-2.0
//

package magent

import (
	"fmt"
	"os"
	"sync"

	"github.com/containerd/containerd/defaults"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/sirupsen/logrus"

	// register grpc event types
	_ "github.com/containerd/containerd/api/events"
)

var magentLog = logrus.WithField("source", "magent")

// SetLogger sets the logger for magent package.
func SetLogger(logger *logrus.Entry) {
	fields := magentLog.Data
	magentLog = logger.WithFields(fields)
}

// MAgent is management agent
type MAgent struct {
	containerdAddr       string
	containerdConfigFile string
	containerdStatePath  string
	sandboxCache         *sandboxCache
}

// NewMAgent create and return a new MAgent instance
func NewMAgent(containerdAddr, containerdConfigFile string) (*MAgent, error) {
	if containerdAddr == "" {
		return nil, fmt.Errorf("Containerd serve address missing.")
	}

	containerdConf := &srvconfig.Config{
		State: defaults.DefaultStateDir,
	}

	if err := srvconfig.LoadConfig(containerdConfigFile, containerdConf); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	ma := &MAgent{
		containerdAddr:       containerdAddr,
		containerdConfigFile: containerdConfigFile,
		containerdStatePath:  containerdConf.State,
		sandboxCache: &sandboxCache{
			Mutex:     &sync.Mutex{},
			sandboxes: make(map[string]string),
		},
	}

	if err := ma.initSandboxCache(); err != nil {
		return nil, err
	}

	// register metrics
	registerMetrics()

	go ma.sandboxCache.startEventsListener(ma.containerdAddr)

	return ma, nil
}

func (ma *MAgent) initSandboxCache() error {
	sandboxes, err := ma.getSandboxes()
	if err != nil {
		return err
	}
	ma.sandboxCache.init(sandboxes)
	return nil
}
