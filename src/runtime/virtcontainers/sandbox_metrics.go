package virtcontainers

import (
	mutils "github.com/kata-containers/kata-containers/src/runtime/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"
)

const namespaceHypervisor = "hypervisor"

var (
	hypervisorThreads = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "threads",
		Help:      "Hypervisor process threads.",
	})

	hypervisorProcStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "proc_status",
		Help:      "Hypervisor proc status.",
	},
		[]string{"mem_type"},
	)

	hypervisorProcStat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "proc_stat",
		Help:      "Hypervisor proc stat.",
	},
		[]string{"item"},
	)

	hypervisorNetdev = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "netdev",
		Help:      "Net devices stats.",
	},
		[]string{"interface", "item"},
	)

	hypervisorIOStat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "io_stat",
		Help:      "Process IO stat.",
	},
		[]string{"item"},
	)

	hypervisorOpenFDs = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespaceHypervisor,
		Name:      "fds",
		Help:      "Open FDs for hypervisor.",
	})
)

func init() {
	prometheus.MustRegister(hypervisorThreads)
	prometheus.MustRegister(hypervisorProcStatus)
	prometheus.MustRegister(hypervisorProcStat)
	prometheus.MustRegister(hypervisorNetdev)
	prometheus.MustRegister(hypervisorIOStat)
	prometheus.MustRegister(hypervisorOpenFDs)
}

// UpdateRuntimeMetrics update shim/hypervisor's metrics
func (s *Sandbox) UpdateRuntimeMetrics() error {
	pids := s.hypervisor.getPids()
	if len(pids) == 0 {
		return nil
	}

	hypervisorPid := pids[0]

	proc, err := procfs.NewProc(hypervisorPid)
	if err != nil {
		return err
	}

	// process FDs
	if fds, err := proc.FileDescriptorsLen(); err == nil {
		hypervisorOpenFDs.Set(float64(fds))
	}

	// process net device stat
	if netdev, err := proc.NetDev(); err == nil {
		// netdev: map[string]NetDevLine
		for _, v := range netdev {
			mutils.SetGaugeVecNetDev(hypervisorNetdev, v)
		}
	}

	// process stat
	if procStat, err := proc.Stat(); err == nil {
		hypervisorThreads.Set(float64(procStat.NumThreads))
		mutils.SetGaugeVecProcStat(hypervisorProcStat, procStat)
	}

	// process status
	if procStatus, err := proc.NewStatus(); err == nil {
		mutils.SetGaugeVecProcStatus(hypervisorProcStatus, procStatus)
	}

	// process IO stat
	if ioStat, err := proc.IO(); err == nil {
		mutils.SetGaugeVecProcIO(hypervisorIOStat, ioStat)
	}

	return nil
}
