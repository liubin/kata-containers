// Copyright (c) 2018 HyperHQ Inc.
//
// SPDX-License-Identifier: Apache-2.0
//

package containerdshim

import (
	"context"

	cgroupsStats "github.com/containerd/cgroups/stats/v1"
	"github.com/containerd/typeurl"

	google_protobuf "github.com/gogo/protobuf/types"
	vc "github.com/kata-containers/kata-containers/src/runtime/virtcontainers"
)

func marshalMetrics(ctx context.Context, s *service, containerID string) (*google_protobuf.Any, error) {
	stats, err := s.sandbox.StatsContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}

	metrics := statsToMetrics(&stats)

	data, err := typeurl.MarshalAny(metrics)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func statsToMetrics(stats *vc.ContainerStats) *cgroupsStats.Metrics {
	metrics := &cgroupsStats.Metrics{}

	if stats.CgroupStats != nil {
		metrics = &cgroupsStats.Metrics{
			Hugetlb: setHugetlbStats(stats.CgroupStats.HugetlbStats),
			Pids:    setPidsStats(stats.CgroupStats.PidsStats),
			CPU:     setCPUStats(stats.CgroupStats.CPUStats),
			Memory:  setMemoryStats(stats.CgroupStats.MemoryStats),
			Blkio:   setBlkioStats(stats.CgroupStats.BlkioStats),
		}
	}

	metrics.Network = setNetworkStats(stats.NetworkStats)

	return metrics
}

func setHugetlbStats(vcHugetlb map[string]vc.HugetlbStats) []*cgroupsStats.HugetlbStat {
	var hugetlbStats []*cgroupsStats.HugetlbStat
	for _, v := range vcHugetlb {
		hugetlbStats = append(
			hugetlbStats,
			&cgroupsStats.HugetlbStat{
				Usage:   v.Usage,
				Max:     v.MaxUsage,
				Failcnt: v.Failcnt,
			})
	}

	return hugetlbStats
}

func setPidsStats(vcPids vc.PidsStats) *cgroupsStats.PidsStat {
	pidsStats := &cgroupsStats.PidsStat{
		Current: vcPids.Current,
		Limit:   vcPids.Limit,
	}

	return pidsStats
}

func setCPUStats(vcCPU vc.CPUStats) *cgroupsStats.CPUStat {

	var perCPU []uint64
	perCPU = append(perCPU, vcCPU.CPUUsage.PercpuUsage...)

	cpuStats := &cgroupsStats.CPUStat{
		Usage: &cgroupsStats.CPUUsage{
			Total:  vcCPU.CPUUsage.TotalUsage,
			Kernel: vcCPU.CPUUsage.UsageInKernelmode,
			User:   vcCPU.CPUUsage.UsageInUsermode,
			PerCPU: perCPU,
		},
		Throttling: &cgroupsStats.Throttle{
			Periods:          vcCPU.ThrottlingData.Periods,
			ThrottledPeriods: vcCPU.ThrottlingData.ThrottledPeriods,
			ThrottledTime:    vcCPU.ThrottlingData.ThrottledTime,
		},
	}

	return cpuStats
}

func setMemoryStats(vcMemory vc.MemoryStats) *cgroupsStats.MemoryStat {
	memoryStats := &cgroupsStats.MemoryStat{
		Usage: &cgroupsStats.MemoryEntry{
			Limit:   vcMemory.Usage.Limit,
			Usage:   vcMemory.Usage.Usage,
			Max:     vcMemory.Usage.MaxUsage,
			Failcnt: vcMemory.Usage.Failcnt,
		},
		Swap: &cgroupsStats.MemoryEntry{
			Limit:   vcMemory.SwapUsage.Limit,
			Usage:   vcMemory.SwapUsage.Usage,
			Max:     vcMemory.SwapUsage.MaxUsage,
			Failcnt: vcMemory.SwapUsage.Failcnt,
		},
		Kernel: &cgroupsStats.MemoryEntry{
			Limit:   vcMemory.KernelUsage.Limit,
			Usage:   vcMemory.KernelUsage.Usage,
			Max:     vcMemory.KernelUsage.MaxUsage,
			Failcnt: vcMemory.KernelUsage.Failcnt,
		},
		KernelTCP: &cgroupsStats.MemoryEntry{
			Limit:   vcMemory.KernelTCPUsage.Limit,
			Usage:   vcMemory.KernelTCPUsage.Usage,
			Max:     vcMemory.KernelTCPUsage.MaxUsage,
			Failcnt: vcMemory.KernelTCPUsage.Failcnt,
		},
	}

	if vcMemory.UseHierarchy {
		memoryStats.Cache = vcMemory.Stats["total_cache"]
		memoryStats.RSS = vcMemory.Stats["total_rss"]
		memoryStats.MappedFile = vcMemory.Stats["total_mapped_file"]
	} else {
		memoryStats.Cache = vcMemory.Stats["cache"]
		memoryStats.RSS = vcMemory.Stats["rss"]
		memoryStats.MappedFile = vcMemory.Stats["mapped_file"]
	}
	if v, ok := vcMemory.Stats["pgfault"]; ok {
		memoryStats.PgFault = v
	}
	if v, ok := vcMemory.Stats["pgmajfault"]; ok {
		memoryStats.PgMajFault = v
	}
	if v, ok := vcMemory.Stats["total_inactive_file"]; ok {
		memoryStats.TotalInactiveFile = v
	}

	return memoryStats
}

func setBlkioStats(vcBlkio vc.BlkioStats) *cgroupsStats.BlkIOStat {
	blkioStats := &cgroupsStats.BlkIOStat{
		IoServiceBytesRecursive: copyBlkio(vcBlkio.IoServiceBytesRecursive),
		IoServicedRecursive:     copyBlkio(vcBlkio.IoServicedRecursive),
		IoQueuedRecursive:       copyBlkio(vcBlkio.IoQueuedRecursive),
		SectorsRecursive:        copyBlkio(vcBlkio.SectorsRecursive),
		IoServiceTimeRecursive:  copyBlkio(vcBlkio.IoServiceTimeRecursive),
		IoWaitTimeRecursive:     copyBlkio(vcBlkio.IoWaitTimeRecursive),
		IoMergedRecursive:       copyBlkio(vcBlkio.IoMergedRecursive),
		IoTimeRecursive:         copyBlkio(vcBlkio.IoTimeRecursive),
	}

	return blkioStats
}

func copyBlkio(s []vc.BlkioStatEntry) []*cgroupsStats.BlkIOEntry {
	ret := make([]*cgroupsStats.BlkIOEntry, len(s))
	for i, v := range s {
		ret[i] = &cgroupsStats.BlkIOEntry{
			Op:    v.Op,
			Major: v.Major,
			Minor: v.Minor,
			Value: v.Value,
		}
	}

	return ret
}

func setNetworkStats(vcNetwork []*vc.NetworkStats) []*cgroupsStats.NetworkStat {
	networkStats := make([]*cgroupsStats.NetworkStat, len(vcNetwork))
	for i, v := range vcNetwork {
		networkStats[i] = &cgroupsStats.NetworkStat{
			Name:      v.Name,
			RxBytes:   v.RxBytes,
			RxPackets: v.RxPackets,
			RxErrors:  v.RxErrors,
			RxDropped: v.RxDropped,
			TxBytes:   v.TxBytes,
			TxPackets: v.TxPackets,
			TxErrors:  v.TxErrors,
			TxDropped: v.TxDropped,
		}
	}

	return networkStats
}
