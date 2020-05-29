package magent

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/defaults"
	srvconfig "github.com/containerd/containerd/services/server/config"
)

// MAgent is management agent
type MAgent struct {
	containerdAddr       string
	containerdConfigFile string
	containerdStatePath  string
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

	return &MAgent{
		containerdAddr:       containerdAddr,
		containerdConfigFile: containerdConfigFile,
		containerdStatePath:  containerdConf.State,
	}, nil
}
