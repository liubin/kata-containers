// Copyright (c) 2019 Ant Financial
//
// SPDX-License-Identifier: Apache-2.0
//

use cgroups::{BlkIoDeviceResource, BlkIoDeviceThrottleResource, 
    Cgroup, CgroupPid, Controller, HugePageResource, NetworkPriority};
use cgroups::blkio::{BlkIo, BlkIoController, BlkIoData, IoService};
use cgroups::cpu::CpuController;
use cgroups::cpuacct::CpuAcctController;
use cgroups::cpuset::CpuSetController;
use cgroups::freezer::{FreezerController, FreezerState};
use cgroups::hugetlb::HugeTlbController;
use cgroups::memory::MemController;
use cgroups::pid::{PidController, PidMax};

use crate::cgroups::Manager as CgroupManager;
use crate::container::DEFAULT_DEVICES;
use crate::errors::*;
use lazy_static;
use libc::{self, pid_t};
use nix::errno::Errno;
use oci::{LinuxDeviceCgroup, LinuxResources, LinuxThrottleDevice, LinuxWeightDevice};
use protobuf::{CachedSize, RepeatedField, SingularPtrField, UnknownFields};
use protocols::agent::{
    BlkioStats, BlkioStatsEntry, CgroupStats, CpuStats, CpuUsage, HugetlbStats, MemoryData,
    MemoryStats, PidsStats, ThrottlingData,
};
use regex::Regex;
use std::collections::HashMap;
use std::fs;
use std::path::Path;

// Convenience macro to obtain the scope logger
macro_rules! sl {
    () => {
        slog_scope::logger().new(o!("subsystem" => "cgroups"))
    };
}

pub fn load_or_create<'a>(v1: &'a dyn cgroups::Hierarchy, path: &str) -> Cgroup<'a> {
    let valid_path = path.trim_start_matches("/").to_string();
    let cg = cgroups::Cgroup::load(v1, valid_path.as_str());
    let cpu_controller: &CpuController = cg.controller_of().unwrap();
    if cpu_controller.exists() {
        cg
    } else {
        cgroups::Cgroup::new(v1, valid_path.as_str())
    }
}

pub fn load<'a>(v1: &'a dyn cgroups::Hierarchy, path: &str) -> Option<Cgroup<'a>> {
    let valid_path = path.trim_start_matches("/").to_string();
    let cg = cgroups::Cgroup::load(v1, valid_path.as_str());
    let cpu_controller: &CpuController = cg.controller_of().unwrap();
    if cpu_controller.exists() {
        Some(cg)
    } else {
        None
    }
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct Manager {
    pub paths: HashMap<String, String>,
    pub mounts: HashMap<String, String>,
    pub rels: HashMap<String, String>,
    pub cpath: String,
}

impl CgroupManager for Manager {
    fn apply(&self, pid: pid_t) -> Result<()> {
        let v1 = cgroups::hierarchies::V1::new();
        let cg = load_or_create(&v1, &self.cpath);
        cg.add_task(CgroupPid::from(pid as u64));
        Ok(())
    }

    fn set(&self, r: &LinuxResources, update: bool) -> Result<()> {
        let res = &mut cgroups::Resources::default();

        // cpuset
        if r.cpu.is_some() {
            let cpu = r.cpu.as_ref().unwrap();
            res.cpu = Default::default();
            res.cpu.update_values=true;
            // For updatecontainer, just set the new value
            if !cpu.cpus.is_empty() {
                res.cpu.cpus = cpu.cpus.clone();
            }

            if !cpu.mems.is_empty() {
                res.cpu.mems = cpu.mems.clone();
            }

            if cpu.shares.is_some() {
                res.cpu.shares = cpu.shares.unwrap();
            }
            if cpu.quota.is_some() {
                res.cpu.quota = cpu.quota.unwrap();
            }
            if cpu.period.is_some() {
                res.cpu.period = cpu.period.unwrap();
            }

            if cpu.realtime_runtime.is_some() {
                res.cpu.realtime_runtime = cpu.realtime_runtime.unwrap();
            }
            if cpu.realtime_period.is_some() {
                res.cpu.realtime_period = cpu.realtime_period.unwrap();
            }
        }

        let memory = r.memory.as_ref().unwrap();
        // FIXME
        // // initialize kmem limits for accounting
        // if !update {
        //     try_write(dir, KMEM_LIMIT, 1)?;
        //     try_write(dir, KMEM_LIMIT, -1)?;
        // }

        if memory.limit.is_some() {
            res.memory.memory_hard_limit =  memory.limit.unwrap() as u64;
        }
        if memory.reservation.is_some() {
            res.memory.memory_soft_limit =  memory.reservation.unwrap() as u64;
        }

        if memory.swap.is_some() {
            res.memory.memory_swap_limit =  memory.swap.unwrap() as u64;
        }
        if memory.kernel.is_some() {
            res.memory.kernel_memory_limit =  memory.kernel.unwrap() as u64;
        }

        if memory.kernel_tcp.is_some() {
            res.memory.kernel_tcp_memory_limit =  memory.kernel_tcp.unwrap() as u64;
        }
        if memory.swapiness.is_some() {
            res.memory.swappiness =  memory.swapiness.unwrap() as u64;
        }


        // FIXME
        // if memory.disable_oom_killer.unwrap_or(false) {
        //     write_file(dir, OOM_CONTROL, 1)?;
        // }


        if r.pids.is_some() {
            let pids = r.pids.as_ref().unwrap();
            let v = if pids.limit > 0 {
                PidMax::Value(pids.limit)
            } else {
                PidMax::Max
            };
            res.pid.maximum_number_of_processes = v;
        }

        if r.block_io.is_some() {
            res.blkio.update_values = true;
            let blkio = r.block_io.as_ref().unwrap();

            if blkio.weight.is_some() {
                res.blkio.weight = blkio.weight.unwrap() as u16;
            }

            if blkio.leaf_weight.is_some() {
                res.blkio.leaf_weight = blkio.leaf_weight.unwrap() as u16;
            }

            let mut vec = vec![];
            for d in blkio.weight_device.iter() {
                if d.weight.is_some() && d.leaf_weight.is_some() {
                    let dr = BlkIoDeviceResource{
                        major: d.blk.major as u64,
                        minor: d.blk.minor as u64,
                        weight: d.weight.unwrap() as u16,
                        leaf_weight: d.leaf_weight.unwrap() as u16,
                    };
                    vec.push(dr);
                }
            }
            res.blkio.weight_device = vec;

            let mut vec = vec![];
            for d in blkio.throttle_read_bps_device.iter() {
                let tr = BlkIoDeviceThrottleResource {
                    major: d.blk.major as u64,
                    minor: d.blk.minor as u64,
                    rate: d.rate as u64,
                };
                vec.push(tr);
            }
            res.blkio.throttle_read_bps_device = vec;

            let mut vec = vec![];
            for d in blkio.throttle_write_bps_device.iter() {
                let tr = BlkIoDeviceThrottleResource {
                    major: d.blk.major as u64,
                    minor: d.blk.minor as u64,
                    rate: d.rate as u64,
                };
                vec.push(tr);
            }
            res.blkio.throttle_write_bps_device = vec;

            let mut vec = vec![];
            for d in blkio.throttle_read_iops_device.iter() {
                let tr = BlkIoDeviceThrottleResource {
                    major: d.blk.major as u64,
                    minor: d.blk.minor as u64,
                    rate: d.rate as u64,
                };
                vec.push(tr);
            }
            res.blkio.throttle_read_iops_device = vec;

            let mut vec = vec![];
            for d in blkio.throttle_write_iops_device.iter() {
                let tr = BlkIoDeviceThrottleResource {
                    major: d.blk.major as u64,
                    minor: d.blk.minor as u64,
                    rate: d.rate as u64,
                };
                vec.push(tr);
            }
            res.blkio.throttle_write_iops_device = vec;
        }

        if r.hugepage_limits.len() > 0 {
            res.hugepages.update_values = true;
            let mut limits = vec![];

            for l in r.hugepage_limits.iter() {

                let hr = HugePageResource{
                    size: l.page_size.clone(),
                    limit: l.limit,
                };
                limits.push(hr);
            }
        }

        if r.network.is_some() {
            let network = r.network.as_ref().unwrap();

            // cls classid
            if network.class_id.is_some() {
                res.network.class_id = network.class_id.unwrap() as u64;
            }

            // priorities
            let mut priorities = vec![];
            for p in network.priorities.iter() {
                priorities.push(NetworkPriority{
                    name: p.name.clone(),
                    priority:p.priority as u64,
                });
            }

            res.network.priorities=priorities;
        }



        Ok(())
    }

    fn get_stats(&self) -> Result<CgroupStats> {
        // CpuStats
        info!(sl!(), "cpu_usage");
        let cpu_usage = get_cpuacct_stats(&self.cpath);

        info!(sl!(), "throttling_data");
        let throttling_data = get_cpu_stats(&self.cpath);

        info!(sl!(), "cpu_stats");
        let cpu_stats =  SingularPtrField::some(CpuStats {
                cpu_usage,
                throttling_data,
                unknown_fields: UnknownFields::default(),
                cached_size: CachedSize::default(),
        });

        // Memorystats
        info!(sl!(), "memory_stats");
        let memory_stats = get_memory_stats(&self.cpath);

        // PidsStats
        info!(sl!(), "pids_stats");
        let pids_stats = get_pids_stats(&self.cpath);

        // BlkioStats
        // note that virtiofs has no blkio stats
        info!(sl!(), "blkio_stats");
        let blkio_stats = get_blkio_stats(&self.cpath);

        // HugetlbStats
        info!(sl!(), "hugetlb_stats");
        let hugetlb_stats = HashMap::new();

        Ok(CgroupStats {
            cpu_stats,
            memory_stats,
            pids_stats,
            blkio_stats,
            hugetlb_stats,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        })
    }

    fn freeze(&self, state: FreezerState) -> Result<()> {
        let v1 = cgroups::hierarchies::V1::new();
        let cg = load_or_create(&v1, &self.cpath);
        let freezer_controller: &FreezerController = cg.controller_of().unwrap();
        match state {
            FreezerState::Thawed => {
                // FIXME handle result
                freezer_controller.thaw();
            },
            FreezerState::Frozen => {
                freezer_controller.freeze();
            },
            _ => {
                return Err(nix::Error::Sys(Errno::EINVAL).into());
            }
        }

        Ok(())
    }

    fn destroy(&mut self) -> Result<()> {
        let v1 = cgroups::hierarchies::V1::new();
        let cg = load(&v1, &self.cpath);
        if cg.is_some(){
            cg.unwrap().delete();
        }
        Ok(())
    }

    fn get_pids(&self) -> Result<Vec<pid_t>> {
        let v1 = cgroups::hierarchies::V1::new();
        let cg = load_or_create(&v1, &self.cpath);
        let mem_controller: &MemController = cg.controller_of().unwrap();
        let pids = mem_controller.tasks();
        let result = pids.iter().map(|x| x.pid as i32).collect::<Vec<i32>>();

        Ok(result)
    }

}


fn line_to_vec(line: &str) ->Vec<u64>{
    let mut m = Vec::new();
    for n in line.split(' ') {
        if !n.trim().is_empty() {
            match n.trim().parse::<u64>() {
                Ok(v) => m.push(v),
                Err(_) => {},
            }
        }
    }

    m
}

fn lines_to_map(lines: &str) -> HashMap<String, u64>{
    let mut m = HashMap::new();
    for line in lines.lines() {
        let t: Vec<&str> = line.split(' ').collect();
        if t.len() != 2 {
            continue;
        }
        match t[1].trim().parse::<u64>() {
            Ok(v) => {m.insert(t[0].to_string(),v);},
            Err(_) =>{},
        }
    }

    m
}

fn get_cpu_stats(dir: &str) -> SingularPtrField<ThrottlingData> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, dir);
    let cpu_controller: &CpuController = cg.controller_of().unwrap();

    // FIXME should get from cpu struct
    let stat = cpu_controller.cpu().stat;

    let h = lines_to_map(&stat);

    SingularPtrField::some(ThrottlingData {
        periods: *h.get("nr_periods").unwrap(),
        throttled_periods: *h.get("nr_throttled").unwrap(),
        throttled_time: *h.get("throttled_time").unwrap(),
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}

pub const NANO_PER_SECOND: u64 = 1000000000;

lazy_static! {
    pub static ref CLOCK_TICKS: f64 = {
        let n = unsafe { libc::sysconf(libc::_SC_CLK_TCK) };

        n as f64
    };
}

fn get_cpuacct_stats(dir: &str) -> SingularPtrField<CpuUsage> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, dir);
    let cpuacct_controller: &CpuAcctController = cg.controller_of().unwrap();

    let cpuacct = cpuacct_controller.cpuacct();

    let h = lines_to_map(&cpuacct.stat);
    let usage_in_usermode =
        (((*h.get("user").unwrap() * NANO_PER_SECOND) as f64) / *CLOCK_TICKS) as u64;
    let usage_in_kernelmode =
        (((*h.get("system").unwrap() * NANO_PER_SECOND) as f64) / *CLOCK_TICKS) as u64;

    let total_usage = cpuacct.usage;

    let percpu_usage = line_to_vec(&cpuacct.usage_percpu);

    SingularPtrField::some(CpuUsage {
        total_usage,
        percpu_usage,
        usage_in_kernelmode,
        usage_in_usermode,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}



fn get_memory_stats(dir: &str) -> SingularPtrField<MemoryStats> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, dir);
    let memory_controller: &MemController = cg.controller_of().unwrap();

    // cache from memory stat
    let memory = memory_controller.memory_stat();
    let cache = memory.stat.cache;

    // use_hierarchy
    let value = memory.use_hierarchy;
    let use_hierarchy = if value == 1 { true } else { false };

    // gte memory datas
    let usage = SingularPtrField::some(MemoryData {
        usage: memory.usage_in_bytes,
        max_usage: memory.max_usage_in_bytes,
        failcnt: memory.fail_cnt,
        limit: memory.limit_in_bytes,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    // get swap usage
    let memswap = memory_controller.memswap();

    let swap_usage = SingularPtrField::some(MemoryData {
        usage: memswap.usage_in_bytes,
        max_usage: memswap.max_usage_in_bytes,
        failcnt: memswap.fail_cnt,
        limit: memswap.limit_in_bytes,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    // get kernel usage
    let kmem_stat = memory_controller.kmem_stat();

    let kernel_usage = SingularPtrField::some(MemoryData {
        usage: kmem_stat.usage_in_bytes,
        max_usage:  kmem_stat.max_usage_in_bytes,
        failcnt: kmem_stat.fail_cnt,
        limit: kmem_stat.limit_in_bytes,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    // FIXME 
    let h = HashMap::<String, u64>::new();

    SingularPtrField::some(MemoryStats {
        cache,
        usage,
        swap_usage,
        kernel_usage,
        use_hierarchy,
        stats: h,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}

fn get_pids_stats(dir: &str) -> SingularPtrField<PidsStats> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, dir);
    let pid_controller: &PidController = cg.controller_of().unwrap();

    let current = pid_controller.get_pid_current().unwrap_or(0);
    let max = pid_controller.get_pid_max();

    let limit = if max.is_err() {
        0
    } else {
        match max.unwrap() {
            PidMax::Value(v) => v,
            PidMax::Max => 0,
        }
    } as u64;

    SingularPtrField::some(PidsStats {
        current,
        limit,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}

/*
examples(from runc):

    blkio.sectors
    8:0 6792

    blkio.io_service_bytes
    8:0 Read 1282048
    8:0 Write 2195456
    8:0 Sync 2195456
    8:0 Async 1282048
    8:0 Total 3477504
    Total 3477504

    blkio.io_serviced
    8:0 Read 124
    8:0 Write 104
    8:0 Sync 104
    8:0 Async 124
    8:0 Total 228
    Total 228

    blkio.io_queued
    8:0 Read 0
    8:0 Write 0
    8:0 Sync 0
    8:0 Async 0
    8:0 Total 0
    Total 0
*/

fn get_blkio_stat_blkiodata(blkiodata: &Vec<BlkIoData>) -> RepeatedField<BlkioStatsEntry> {
    let mut m = RepeatedField::new();
    if blkiodata.len() == 0 {
        return m;
    }

    for d in blkiodata {
        m.push(BlkioStatsEntry {
            major: d.major as u64,
            minor:d.minor as u64,
            op: "".to_string(),
            value: d.data,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });
    }

    m
}

fn get_blkio_stat_ioservice(services: &Vec<IoService>) -> RepeatedField<BlkioStatsEntry> {
    let mut m = RepeatedField::new();

    // do as runc
    if services.len() == 0 {
        return m;
    }

    for s  in services {
        // FIXME lost discard
        // https://docs.rs/cgroups/0.1.0/src/cgroups/blkio.rs.html#74

        // Read
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor:s.minor as u64,
            op: "Read".to_string(),
            value: s.read,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Write
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor:s.minor as u64,
            op: "Write".to_string(),
            value: s.write,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Sync
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor:s.minor as u64,
            op: "Sync".to_string(),
            value: s.sync,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Async
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor:s.minor as u64,
            op: "Async".to_string(),
            value: s.r#async,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

    }
    m
}

fn get_blkio_stats(dir: &str) -> SingularPtrField<BlkioStats> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, dir);
    let blkio_controller: &BlkIoController = cg.controller_of().unwrap();
    let blkio = blkio_controller.blkio();

    let mut m = BlkioStats::new();
    let io_serviced_recursive = blkio.io_serviced_recursive;

    if io_serviced_recursive.len() == 0 {
        // fall back to generic stats
        // blkio.throttle.io_service_bytes,
        // maybe io_service_bytes_recursive?
        // stick to runc for now
        m.io_service_bytes_recursive = get_blkio_stat_ioservice(&blkio.throttle.io_service_bytes);
        m.io_serviced_recursive = get_blkio_stat_ioservice(&blkio.throttle.io_serviced);
    } else {
        // cfq stats
        // IoService
        m.io_service_bytes_recursive = get_blkio_stat_ioservice(&blkio.io_service_bytes_recursive);
        m.io_serviced_recursive = get_blkio_stat_ioservice(&io_serviced_recursive);
        m.io_queued_recursive = get_blkio_stat_ioservice(&blkio.io_queued_recursive);
        m.io_service_time_recursive = get_blkio_stat_ioservice(&blkio.io_service_time_recursive);
        m.io_wait_time_recursive = get_blkio_stat_ioservice(&blkio.io_wait_time_recursive);
        m.io_merged_recursive = get_blkio_stat_ioservice(&blkio.io_merged_recursive);


        // BlkIoData
        m.io_time_recursive = get_blkio_stat_blkiodata(&blkio.time_recursive);
        m.sectors_recursive = get_blkio_stat_blkiodata(&blkio.sectors_recursive);
    }

    SingularPtrField::some(m)
}



// fn get_hugetlb_stats(dir: &str) -> SingularPtrField<HashMap<String, HugetlbStats>> {
//     let v1 = cgroups::hierarchies::V1::new();
//     let cg = load_or_create(&v1, dir);
//     let hugetlb_controller: &HugeTlbController = cg.controller_of().unwrap();

//     let mut h = HashMap::new();
//     for pagesize in HUGEPAGESIZES.iter() {
//         let fusage = format!("{}.{}.{}", HUGETLB_BASE, pagesize, HUGETLB_USAGE);
//         let fmax = format!("{}.{}.{}", HUGETLB_BASE, pagesize, HUGETLB_MAX_USAGE);
//         let ffailcnt = format!("{}.{}.{}", HUGETLB_BASE, pagesize, HUGETLB_FAILCNT);

//         let usage = get_param_u64(dir, fusage.as_str())?;
//         let max_usage = get_param_u64(dir, fmax.as_str())?;
//         let failcnt = get_param_u64(dir, ffailcnt.as_str())?;

//         h.insert(
//             pagesize.to_string(),
//             HugetlbStats {
//                 usage,
//                 max_usage,
//                 failcnt,
//                 unknown_fields: UnknownFields::default(),
//                 cached_size: CachedSize::default(),
//             },
//         );
//     }

//     SingularPtrField::some(h)
// }


pub const PATHS: &'static str = "/proc/self/cgroup";
pub const MOUNTS: &'static str = "/proc/self/mountinfo";

fn get_paths() -> Result<HashMap<String, String>> {
    let mut m = HashMap::new();
    for l in fs::read_to_string(PATHS)?.lines() {
        let fl: Vec<&str> = l.split(':').collect();
        if fl.len() != 3 {
            info!(sl!(), "Corrupted cgroup data!");
            continue;
        }

        let keys: Vec<&str> = fl[1].split(',').collect();
        for key in &keys {
            m.insert(key.to_string(), fl[2].to_string());
        }
    }
    Ok(m)
}

fn get_mounts() -> Result<HashMap<String, String>> {
    let mut m = HashMap::new();
    let paths = get_paths()?;

    for l in fs::read_to_string(MOUNTS)?.lines() {
        let p: Vec<&str> = l.split(" - ").collect();
        let pre: Vec<&str> = p[0].split(' ').collect();
        let post: Vec<&str> = p[1].split(' ').collect();

        if post.len() != 3 {
            warn!(sl!(), "mountinfo corrupted!");
            continue;
        }

        if post[0] != "cgroup" && post[0] != "cgroup2" {
            continue;
        }

        let names: Vec<&str> = post[2].split(',').collect();

        for name in &names {
            if paths.contains_key(*name) {
                m.insert(name.to_string(), pre[4].to_string());
            }
        }
    }

    Ok(m)
}

impl Manager {
    pub fn new(cpath: &str) -> Result<Self> {
        let mut m = HashMap::new();

        if !cpath.starts_with('/') {
            return Err(nix::Error::Sys(Errno::EINVAL).into());
        }

        let paths = get_paths()?;
        let mounts = get_mounts()?;

        for (key, value) in &paths {
            let mnt = mounts.get(key);

            if mnt.is_none() {
                continue;
            }

            let p = if value == "/" {
                format!("{}{}", mnt.unwrap(), cpath)
            } else {
                format!("{}{}{}", mnt.unwrap(), value, cpath)
            };

            m.insert(key.to_string(), p);
        }

        Ok(Self {
            paths: m,
            mounts,
            rels: paths,
            cpath: cpath.to_string(),
        })
    }

    pub fn update_cpuset_path(&self, cpuset_cpus: &str) -> Result<()> {
        let v1 = cgroups::hierarchies::V1::new();
        let root_cg = load_or_create(&v1, "");
        let root_cpuset_controller: &CpuSetController = root_cg.controller_of().unwrap();
        let path = root_cpuset_controller.path();
        let root_path = Path::new(path);
        debug!(sl!(),"root cpuset path: {:?}" , &path);

        let cg = load_or_create(&v1, &self.cpath);
        let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();
        let path = cpuset_controller.path();
        let container_path = Path::new(path);
        debug!(sl!(),"container cpuset path: {:?}" , &path);

        let mut paths = vec![];
        for ancestor in container_path.ancestors() {
            if ancestor == root_path {
                break;
            }
            if ancestor!= container_path {
                paths.push(ancestor);
            }
        }
        debug!(sl!(),"paths to update cpuset: {:?}" , &paths);

        let mut i = paths.len() ;
        loop  {
            i = i -1;
            let cg = load_or_create(&v1, &paths[i].to_str().unwrap());
            let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();
            // FIXME handle result
            cpuset_controller.set_cpus(cpuset_cpus);
            if i < 0 {
                break
            }
        }

        Ok(())
    }
}

pub fn get_guest_cpuset() -> Result<String> {
    let v1 = cgroups::hierarchies::V1::new();
    let cg = load_or_create(&v1, "");
    let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();
    let cpu_set = cpuset_controller.cpuset();
    let cpus = cpu_set.cpus.iter().map(|x| if x.0 == x.1 { format!("{}",x.0)} else { format!("{}-{}", x.0,x.1)}).collect::<Vec<String>>();

    let cpu_string = cpus.join(",");
    info!(sl!(), "get_guest_cpuset: {}", &cpus.join(","));
    Ok(cpu_string)
}

