package containerdshim

import (
	mutils "github.com/kata-containers/kata-containers/src/runtime/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"
)

const namespaceKatashim = "kata_shim"

var (
	rpcDurationsHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespaceKatashim,
		Name:      "rpc_durations_histogram_million_seconds",
		Help:      "RPC latency distributions.",
		Buckets:   prometheus.ExponentialBuckets(1, 2, 10),
	},
		[]string{"action"},
	)

	katashimThreads = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "threads",
		Help:      "katashim process threads.",
	})

	katashimProcStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "proc_status",
		Help:      "katashim proc status.",
	},
		[]string{"mem_type"},
	)

	katashimProcStat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "proc_stat",
		Help:      "katashim proc stat.",
	},
		[]string{"item"},
	)

	katashimNetdev = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "netdev",
		Help:      "Net devices stats.",
	},
		[]string{"interface", "item"},
	)

	katashimIOStat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "io_stat",
		Help:      "Process IO stat.",
	},
		[]string{"item"},
	)

	katashimOpenFDs = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespaceKatashim,
		Name:      "fds",
		Help:      "Open FDs for katashim.",
	})
)

func init() {
	prometheus.MustRegister(rpcDurationsHistogram)
	prometheus.MustRegister(katashimThreads)
	prometheus.MustRegister(katashimProcStatus)
	prometheus.MustRegister(katashimProcStat)
	prometheus.MustRegister(katashimNetdev)
	prometheus.MustRegister(katashimIOStat)
	prometheus.MustRegister(katashimOpenFDs)
}

func updateShimMetrics() error {
	proc, err := procfs.Self()
	if err != nil {
		return err
	}

	if fds, err := proc.FileDescriptorsLen(); err == nil {
		katashimOpenFDs.Set(float64(fds))
	}

	if netdev, err := proc.NetDev(); err == nil {
		// netdev: map[string]NetDevLine
		for _, v := range netdev {
			mutils.SetGaugeVecNetDev(katashimNetdev, v)
		}
	}

	if procStat, err := proc.Stat(); err == nil {
		katashimThreads.Set(float64(procStat.NumThreads))
		mutils.SetGaugeVecProcStat(katashimProcStat, procStat)
	}

	if procStatus, err := proc.NewStatus(); err == nil {
		mutils.SetGaugeVecProcStatus(katashimProcStatus, procStatus)
	}

	if ioStat, err := proc.IO(); err == nil {
		mutils.SetGaugeVecProcIO(katashimIOStat, ioStat)
	}

	return nil
}
