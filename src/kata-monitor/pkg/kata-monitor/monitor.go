// Copyright (c) 2020 Ant Financial
//
// SPDX-License-Identifier: Apache-2.0
//

package katamonitor

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var monitorLog = logrus.WithField("source", "kata-monitor")

const (
	RuntimeContainerd = "containerd"
	RuntimeCRIO       = "cri-o"
	DefaultRuntime    = RuntimeContainerd
)

// SetLogger sets the logger for katamonitor package.
func SetLogger(logger *logrus.Entry) {
	fields := monitorLog.Data
	monitorLog = logger.WithFields(fields)
}

// KataMonitor is monitor agent
type KataMonitor struct {
	runtimeEndpoint string
	sandboxCache    *sandboxCache
}

// NewKataMonitor create and return a new KataMonitor instance
func NewKataMonitor(runtimeEndpoint string) (*KataMonitor, error) {
	fmt.Println(runtimeEndpoint)

	if !strings.HasPrefix(runtimeEndpoint, "unix") {
		runtimeEndpoint = "unix://" + runtimeEndpoint
	}
	fmt.Println(runtimeEndpoint)
	km := &KataMonitor{
		runtimeEndpoint: runtimeEndpoint,
		sandboxCache: &sandboxCache{
			Mutex:     &sync.Mutex{},
			sandboxes: make(map[string]string),
		},
	}

	// register metrics
	registerMetrics()

	go km.startPodCacheUpdater()

	return km, nil
}

// startPodCacheUpdater will boot a thread to listen container events to manage sandbox cache
func (km *KataMonitor) startPodCacheUpdater() {
	for {
		sandboxes, err := km.getSandboxes()
		if err != nil {
			monitorLog.WithError(err).Error("failed to gete sandboxes")
		}
		monitorLog.WithField("count", len(sandboxes)).Debug("update sandboxes list")
		km.sandboxCache.set(sandboxes)
		time.Sleep(30 * time.Second)
	}
}

// GetAgentURL returns agent URL
func (km *KataMonitor) GetAgentURL(w http.ResponseWriter, r *http.Request) {
	sandboxID, err := getSandboxIdFromReq(r)
	if err != nil {
		commonServeError(w, http.StatusBadRequest, err)
		return
	}
	runtime, err := km.getSandboxRuntime(sandboxID)
	if err != nil {
		commonServeError(w, http.StatusBadRequest, err)
		return
	}

	data, err := km.doGet(sandboxID, runtime, defaultTimeout, "agent-url")
	if err != nil {
		commonServeError(w, http.StatusBadRequest, err)
		return
	}

	fmt.Fprintln(w, string(data))
}

// ListSandboxes list all sandboxes running in Kata
func (km *KataMonitor) ListSandboxes(w http.ResponseWriter, r *http.Request) {
	sandboxes := km.getSandboxList()
	for _, s := range sandboxes {
		w.Write([]byte(fmt.Sprintf("%s\n", s)))
	}
}

func (km *KataMonitor) getSandboxList() []string {
	sn := km.sandboxCache.getAllSandboxes()
	result := make([]string, len(sn))

	i := 0
	for k := range sn {
		result[i] = k
		i++
	}
	return result
}

func (km *KataMonitor) getSandboxRuntime(sandbox string) (string, error) {
	return km.sandboxCache.getSandboxRuntime(sandbox)
}
