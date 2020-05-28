package utils

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/procfs"
)

// SetGaugeVecNetDev set gauge for NetDevLine
func SetGaugeVecNetDev(gv *prometheus.GaugeVec, v procfs.NetDevLine) {
	gv.WithLabelValues(v.Name, "RxBytes").Set(float64(v.RxBytes))
	gv.WithLabelValues(v.Name, "RxPackets").Set(float64(v.RxPackets))
	gv.WithLabelValues(v.Name, "RxErrors").Set(float64(v.RxErrors))
	gv.WithLabelValues(v.Name, "RxDropped").Set(float64(v.RxDropped))

	gv.WithLabelValues(v.Name, "TxBytes").Set(float64(v.TxBytes))
	gv.WithLabelValues(v.Name, "TxPackets").Set(float64(v.TxPackets))
	gv.WithLabelValues(v.Name, "TxErrors").Set(float64(v.TxErrors))
	gv.WithLabelValues(v.Name, "TxDropped").Set(float64(v.TxDropped))
	gv.WithLabelValues(v.Name, "TxCollisions").Set(float64(v.TxCollisions))
	gv.WithLabelValues(v.Name, "TxCarrier").Set(float64(v.TxCarrier))
}

// SetGaugeVecProcStatus set gauge for ProcStatus
func SetGaugeVecProcStatus(gv *prometheus.GaugeVec, procStatus procfs.ProcStatus) {
	gv.WithLabelValues("VmPeak").Set(float64(procStatus.VmPeak))
	gv.WithLabelValues("VmSize").Set(float64(procStatus.VmSize))
	gv.WithLabelValues("VmLck").Set(float64(procStatus.VmLck))
	gv.WithLabelValues("VmPin").Set(float64(procStatus.VmPin))
	gv.WithLabelValues("VmPeak").Set(float64(procStatus.VmPeak))
	gv.WithLabelValues("VmHWM").Set(float64(procStatus.VmHWM))
	gv.WithLabelValues("VmRSS").Set(float64(procStatus.VmRSS))
	gv.WithLabelValues("RssAnon").Set(float64(procStatus.RssAnon))
	gv.WithLabelValues("RssFile").Set(float64(procStatus.RssFile))
	gv.WithLabelValues("RssShmem").Set(float64(procStatus.RssShmem))
	gv.WithLabelValues("VmData").Set(float64(procStatus.VmData))
	gv.WithLabelValues("VmStk").Set(float64(procStatus.VmStk))
	gv.WithLabelValues("VmExe").Set(float64(procStatus.VmExe))
	gv.WithLabelValues("VmLib").Set(float64(procStatus.VmLib))
	gv.WithLabelValues("VmPTE").Set(float64(procStatus.VmPTE))
	gv.WithLabelValues("VmPMD").Set(float64(procStatus.VmPMD))
	gv.WithLabelValues("VmSwap").Set(float64(procStatus.VmSwap))
	gv.WithLabelValues("HugetlbPages").Set(float64(procStatus.HugetlbPages))
	gv.WithLabelValues("VoluntaryCtxtSwitches").Set(float64(procStatus.VoluntaryCtxtSwitches))
	gv.WithLabelValues("NonVoluntaryCtxtSwitches").Set(float64(procStatus.NonVoluntaryCtxtSwitches))
}

// SetGaugeVecProcIO set gauge for ProcIO
func SetGaugeVecProcIO(gv *prometheus.GaugeVec, ioStat procfs.ProcIO) {
	gv.WithLabelValues("RChar").Set(float64(ioStat.RChar))
	gv.WithLabelValues("WChar").Set(float64(ioStat.WChar))
	gv.WithLabelValues("SyscR").Set(float64(ioStat.SyscR))
	gv.WithLabelValues("SyscW").Set(float64(ioStat.SyscW))
	gv.WithLabelValues("ReadBytes").Set(float64(ioStat.ReadBytes))
	gv.WithLabelValues("WriteBytes").Set(float64(ioStat.WriteBytes))
	gv.WithLabelValues("CancelledWriteBytes").Set(float64(ioStat.CancelledWriteBytes))
}

// SetGaugeVecProcStat set gauge for ProcStat
func SetGaugeVecProcStat(gv *prometheus.GaugeVec, procStat procfs.ProcStat) {
	gv.WithLabelValues("UTime").Set(float64(procStat.UTime))
	gv.WithLabelValues("STime").Set(float64(procStat.STime))
	gv.WithLabelValues("CUTime").Set(float64(procStat.CUTime))
	gv.WithLabelValues("CSTime").Set(float64(procStat.CSTime))
}
