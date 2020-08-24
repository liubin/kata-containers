// Copyright (c) 2019, 2020 Ant Financial
//
// SPDX-License-Identifier: Apache-2.0
//

use cgroups::blkio::{BlkIo, BlkIoController, BlkIoData, IoService};
use cgroups::cpu::CpuController;
use cgroups::cpuacct::CpuAcctController;
use cgroups::cpuset::CpuSetController;
use cgroups::devices::DevicePermissions;
use cgroups::devices::DeviceType;
use cgroups::freezer::{FreezerController, FreezerState};
use cgroups::hugetlb::HugeTlbController;
use cgroups::memory::MemController;
use cgroups::pid::PidController;
use cgroups::{
    BlkIoDeviceResource, BlkIoDeviceThrottleResource, Cgroup, CgroupPid, Controller,
    DeviceResource, DeviceResources, HugePageResource, MaxValue, NetworkPriority,
};

use crate::cgroups::Manager as CgroupManager;
use crate::container::DEFAULT_DEVICES;
use crate::errors::*;
use lazy_static;
use libc::{self, pid_t};
use nix::errno::Errno;
use oci::{LinuxDevice, LinuxDeviceCgroup, LinuxResources, LinuxThrottleDevice, LinuxWeightDevice};

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

pub fn load_or_create<'a>(h: Box<&'a dyn cgroups::Hierarchy>, path: &str, relative_paths: HashMap<String, String>) -> Cgroup<'a> {
    let valid_path = path.trim_start_matches("/").to_string();
    let cg = load(h.clone(), &valid_path, relative_paths.clone());
    if cg.is_none(){
        info!(sl!(), "create new cgroup: {}, relative_paths: {:?}", &valid_path, &relative_paths);
        cgroups::Cgroup::new_with_relative_paths(h, valid_path.as_str(), relative_paths)
    }else{
        cg.unwrap()
    }
}

pub fn load<'a>(h: Box<&'a dyn cgroups::Hierarchy>, path: &str, relative_paths: HashMap<String, String>) -> Option<Cgroup<'a>> {
    let valid_path = path.trim_start_matches("/").to_string();
    let cg = cgroups::Cgroup::load_with_relative_paths(h, valid_path.as_str(), relative_paths);
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
        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load_or_create(h, &self.cpath, self.rels.clone());
        cg.add_task(CgroupPid::from(pid as u64));
        Ok(())
    }

    fn set(&self, r: &LinuxResources, update: bool) -> Result<()> {
        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load_or_create(h, &self.cpath, self.rels.clone());
        info!(sl!(), "cgroup manager set : {:?}", r);

        let res = &mut cgroups::Resources::default();

        // cpuset
        if r.cpu.is_some() {
            info!(sl!(), "cgroup manager set cpu !!!");
            let cpu = r.cpu.as_ref().unwrap();

            let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();

            if !cpu.cpus.is_empty() {
                cpuset_controller.set_cpus(&cpu.cpus);
            }

            if !cpu.mems.is_empty() {
                // res.cpu.mems = cpu.mems.clone();
                cpuset_controller.set_mems(&cpu.mems);
            }

            let cpu_controller: &CpuController = cg.controller_of().unwrap();

            let mut shares = cpu.shares.unwrap_or(0);
            if shares !=0 {
                if cg.v2() {
                    shares = convert_shares_to_v2_value(shares);
                }
                cpu_controller.set_shares(shares);
            }

            let quota = cpu.quota.unwrap_or(0) as u64;
            let period = cpu.period.unwrap_or(0);
            cpu_controller.set_cfs_quota_and_period(quota, period);

            let realtime_runtime = cpu.realtime_runtime.unwrap_or(0);
            if realtime_runtime !=0 {
                cpu_controller.set_rt_runtime(realtime_runtime);
            }
            let realtime_period = cpu.realtime_period.unwrap_or(0);
            if realtime_period !=0 {
                cpu_controller.set_rt_period_us(realtime_period);
            }
        }

        if r.memory.is_some() {
            info!(sl!(), "cgroup manager set memory !!!");
            let mem_controller: &MemController = cg.controller_of().unwrap();
            let memory = r.memory.as_ref().unwrap();

            if !update{
                // initialize kmem limits for accounting
                mem_controller.set_kmem_limit(1);
                mem_controller.set_kmem_limit(-1);
            }

            let limit = memory.limit.unwrap_or(0);
            if limit!=0 {
                mem_controller.set_limit(limit);
            }

            let reservation = memory.reservation.unwrap_or(0);
            if reservation !=0 {
                mem_controller.set_soft_limit(reservation);
            }

            let swap = memory.swap.unwrap_or(0);
            if cg.v2() {
                let swap = convert_memory_swap_to_v2_value(swap, limit)?;
            }
            mem_controller.set_memswap_limit(swap);

            let kernel = memory.kernel.unwrap_or(0);
            if kernel!=0 {
                mem_controller.set_kmem_limit(kernel);
            }

            let kernel_tcp = memory.kernel_tcp.unwrap_or(0);
            if kernel_tcp!=0 {
                mem_controller.set_tcp_limit(kernel_tcp);
            }

            if memory.swapiness.unwrap_or(0) <= 100 {
                mem_controller.set_swappiness(memory.swapiness.unwrap_or(0) as u64);
            }
            if memory.disable_oom_killer.unwrap_or(false) {
                match mem_controller.disable_oom_killer() {
                    Ok(_) => {}
                    Err(err) => {
                        error!(sl!(), "failed to disable_oom_killer: {:?}", err);
                    }
                }
            }
            // res.memory.update_values = true;
        }

        if r.pids.is_some() {
            info!(sl!(), "cgroup manager set pids !!!");
            let pid_controller: &PidController = cg.controller_of().unwrap();
            let pids = r.pids.as_ref().unwrap();
            let v = if pids.limit > 0 {
                MaxValue::Value(pids.limit)
            } else {
                MaxValue::Max
            };
            pid_controller.set_pid_max(v);
            // res.pid.maximum_number_of_processes = v;
            // res.pid.update_values = true;
        }

        if r.block_io.is_some() {
            info!(sl!(), "cgroup manager set block !!!");
            res.blkio.update_values = true;
            let blkio = r.block_io.as_ref().unwrap();

            // let blkio_controller: &BlkIoController = cg.controller_of().unwrap();

            let weight= blkio.weight.unwrap_or(0) as u16;
            if weight !=0{
                res.blkio.weight = weight;
            }

            let leaf_weight= blkio.leaf_weight.unwrap_or(0) as u16;
            if leaf_weight !=0{
                res.blkio.leaf_weight = leaf_weight;
            }

            let mut vec = vec![];
            for d in blkio.weight_device.iter() {
                if d.weight.is_some() && d.leaf_weight.is_some() {
                    let dr = BlkIoDeviceResource {
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
            info!(sl!(), "cgroup manager set hugepage !!!");
            res.hugepages.update_values = true;
            let mut limits = vec![];

            for l in r.hugepage_limits.iter() {
                let hr = HugePageResource {
                    size: l.page_size.clone(),
                    limit: l.limit,
                };
                limits.push(hr);
            }
            res.hugepages.limits = limits;
        }

        if r.network.is_some() {
            info!(sl!(), "cgroup manager set network !!!");
            let network = r.network.as_ref().unwrap();

            // cls classid
            let class_id = network.class_id.unwrap_or(0) as u64;
            if class_id != 0 {
                res.network.class_id = class_id;
            }

            // priorities
            let mut priorities = vec![];
            for p in network.priorities.iter() {
                priorities.push(NetworkPriority {
                    name: p.name.clone(),
                    priority: p.priority as u64,
                });
            }

            res.network.update_values = true;
            res.network.priorities = priorities;
        }

        // devices
        let mut devices = vec![];

        for d in r.devices.iter() {
            let dev = linux_device_group_to_cgroup_device(&d);
            devices.push(dev);
        }

        for d in DEFAULT_DEVICES.iter() {
            let dev = linux_device_to_cgroup_device(&d);
            devices.push(dev);
        }

        for d in DEFAULT_ALLOWED_DEVICES.iter() {
            let dev = linux_device_group_to_cgroup_device(&d);
            devices.push(dev);
        }

        res.devices.update_values = true;
        res.devices.devices = devices;

        cg.apply(res);
        Ok(())
    }

    fn get_stats(&self) -> Result<CgroupStats> {
        // CpuStats
        let cpu_usage = get_cpuacct_stats(&self.cpath, &self.rels);

        let throttling_data = get_cpu_stats(&self.cpath, &self.rels);

        let cpu_stats = SingularPtrField::some(CpuStats {
            cpu_usage,
            throttling_data,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Memorystats
        let memory_stats = get_memory_stats(&self.cpath, &self.rels);

        // PidsStats
        let pids_stats = get_pids_stats(&self.cpath, &self.rels);

        // BlkioStats
        // note that virtiofs has no blkio stats
        let blkio_stats = get_blkio_stats(&self.cpath, &self.rels);

        // HugetlbStats
        let hugetlb_stats = get_hugetlb_stats(&self.cpath, &self.rels);

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
        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load_or_create(h, &self.cpath, self.rels.clone());
        let freezer_controller: &FreezerController = cg.controller_of().unwrap();
        match state {
            FreezerState::Thawed => {
                freezer_controller.thaw();
            }
            FreezerState::Frozen => {
                freezer_controller.freeze();
            }
            _ => {
                return Err(nix::Error::Sys(Errno::EINVAL).into());
            }
        }

        Ok(())
    }

    fn destroy(&mut self) -> Result<()> {
        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load(h, &self.cpath, self.rels.clone());
        if cg.is_some() {
            cg.unwrap().delete();
        }
        Ok(())
    }

    fn get_pids(&self) -> Result<Vec<pid_t>> {
        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load_or_create(h, &self.cpath, self.rels.clone());
        let mem_controller: &MemController = cg.controller_of().unwrap();
        let pids = mem_controller.tasks();
        let result = pids.iter().map(|x| x.pid as i32).collect::<Vec<i32>>();

        Ok(result)
    }
}

fn string_to_device_type(s: &String) -> DeviceType {
    match s.as_str() {
        "c" => DeviceType::Char,
        "b" => DeviceType::Block,
        _ => DeviceType::All,
    }
}

fn linux_device_to_cgroup_device(d: &LinuxDevice) -> DeviceResource {
    let dev_type = string_to_device_type(&d.r#type);

    let mut permissions = vec![
        DevicePermissions::Read,
        DevicePermissions::Write,
        DevicePermissions::MkNod,
    ];

    DeviceResource {
        allow: true,
        devtype: dev_type,
        major: d.major,
        minor: d.minor,
        access: permissions,
    }
}

fn linux_device_group_to_cgroup_device(d: &LinuxDeviceCgroup) -> DeviceResource {
    let dev_type = string_to_device_type(&d.r#type);

    let mut permissions: Vec<DevicePermissions> = vec![];
    for p in d.access.chars().collect::<Vec<char>>() {
        match p {
            'r' => permissions.push(DevicePermissions::Read),
            'w' => permissions.push(DevicePermissions::Write),
            'm' => permissions.push(DevicePermissions::MkNod),
            _ => {}
        }
    }

    DeviceResource {
        allow: d.allow,
        devtype: dev_type,
        major: d.major.unwrap_or(0),
        minor: d.minor.unwrap_or(0),
        access: permissions,
    }
}

fn line_to_vec(line: &str) -> Vec<u64> {
    let mut m = Vec::new();
    for n in line.split(' ') {
        if !n.trim().is_empty() {
            match n.trim().parse::<u64>() {
                Ok(v) => m.push(v),
                Err(_) => {}
            }
        }
    }

    m
}

fn lines_to_map(lines: &str) -> HashMap<String, u64> {
    let mut m = HashMap::new();
    for line in lines.lines() {
        let t: Vec<&str> = line.split(' ').collect();
        if t.len() != 2 {
            continue;
        }
        match t[1].trim().parse::<u64>() {
            Ok(v) => {
                m.insert(t[0].to_string(), v);
            }
            Err(_) => {}
        }
    }

    m
}

pub const NANO_PER_SECOND: u64 = 1000000000;
pub const WILDCARD: i64 = -1;

lazy_static! {
    pub static ref CLOCK_TICKS: f64 = {
        let n = unsafe { libc::sysconf(libc::_SC_CLK_TCK) };

        n as f64
    };

    // FIXME chagne to cgroup::DeviceResource
    pub static ref DEFAULT_ALLOWED_DEVICES: Vec<LinuxDeviceCgroup> = {
        let mut v = Vec::new();
        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "c".to_string(),
            major: Some(WILDCARD),
            minor: Some(WILDCARD),
            access: "m".to_string(),
        });

        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "b".to_string(),
            major: Some(WILDCARD),
            minor: Some(WILDCARD),
            access: "m".to_string(),
        });

        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "c".to_string(),
            major: Some(5),
            minor: Some(1),
            access: "rwm".to_string(),
        });

        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "c".to_string(),
            major: Some(136),
            minor: Some(WILDCARD),
            access: "rwm".to_string(),
        });

        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "c".to_string(),
            major: Some(5),
            minor: Some(2),
            access: "rwm".to_string(),
        });

        v.push(LinuxDeviceCgroup {
            allow: true,
            r#type: "c".to_string(),
            major: Some(10),
            minor: Some(200),
            access: "rwm".to_string(),
        });

        v
    };
}

fn get_cpu_stats(dir: &str, relative_paths: &HashMap<String, String>) -> SingularPtrField<ThrottlingData> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());
    let cpu_controller: &CpuController = cg.controller_of().unwrap();

    let stat = cpu_controller.cpu().stat;

    let h = lines_to_map(&stat);

    SingularPtrField::some(ThrottlingData {
        periods: *h.get("nr_periods").unwrap_or(&0),
        throttled_periods: *h.get("nr_throttled").unwrap_or(&0),
        throttled_time: *h.get("throttled_time").unwrap_or(&0),
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}

fn get_cpuacct_stats(dir: &str, relative_paths: &HashMap<String, String>) -> SingularPtrField<CpuUsage> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());
    let cpuacct_controller: Option<&CpuAcctController> = cg.controller_of();
    if cpuacct_controller.is_none() {
        if cg.v2() {
            return SingularPtrField::some(CpuUsage {
                total_usage: 0,
                percpu_usage: vec![],
                usage_in_kernelmode: 0,
                usage_in_usermode: 0,
                unknown_fields: UnknownFields::default(),
                cached_size: CachedSize::default(),
            });
        }

        // try to get from cpu controller
        let cpu_controller: &CpuController = cg.controller_of().unwrap();
        let stat = cpu_controller.cpu().stat;
        let h = lines_to_map(&stat);
        let usage_in_usermode =*h.get("user_usec").unwrap();
        let usage_in_kernelmode =*h.get("system_usec").unwrap();
        let total_usage =*h.get("usage_usec").unwrap();
        let percpu_usage = vec![];

        return SingularPtrField::some(CpuUsage {
            total_usage,
            percpu_usage,
            usage_in_kernelmode,
            usage_in_usermode,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });
    }

    let cpuacct_controller = cpuacct_controller.unwrap();
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

fn get_memory_stats(dir: &str, relative_paths: &HashMap<String, String>) -> SingularPtrField<MemoryStats> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());
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
        limit: memory.limit_in_bytes as u64,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    // get swap usage
    let memswap = memory_controller.memswap();

    let swap_usage = SingularPtrField::some(MemoryData {
        usage: memswap.usage_in_bytes,
        max_usage: memswap.max_usage_in_bytes,
        failcnt: memswap.fail_cnt,
        limit: memswap.limit_in_bytes as u64,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    // get kernel usage
    let kmem_stat = memory_controller.kmem_stat();

    let kernel_usage = SingularPtrField::some(MemoryData {
        usage: kmem_stat.usage_in_bytes,
        max_usage: kmem_stat.max_usage_in_bytes,
        failcnt: kmem_stat.fail_cnt,
        limit: kmem_stat.limit_in_bytes as u64,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    });

    SingularPtrField::some(MemoryStats {
        cache,
        usage,
        swap_usage,
        kernel_usage,
        use_hierarchy,
        stats: memory.stat.raw,
        unknown_fields: UnknownFields::default(),
        cached_size: CachedSize::default(),
    })
}

fn get_pids_stats(dir: &str, relative_paths: &HashMap<String, String>) -> SingularPtrField<PidsStats> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());
    let pid_controller: &PidController = cg.controller_of().unwrap();

    let current = pid_controller.get_pid_current().unwrap_or(0);
    let max = pid_controller.get_pid_max();

    let limit = if max.is_err() {
        0
    } else {
        match max.unwrap() {
            MaxValue::Value(v) => v,
            MaxValue::Max => 0,
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
            minor: d.minor as u64,
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

    for s in services {
        // FIXME lost discard
        // https://docs.rs/cgroups/0.1.0/src/cgroups/blkio.rs.html#74

        // Read
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor: s.minor as u64,
            op: "Read".to_string(),
            value: s.read,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Write
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor: s.minor as u64,
            op: "Write".to_string(),
            value: s.write,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Sync
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor: s.minor as u64,
            op: "Sync".to_string(),
            value: s.sync,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });

        // Async
        m.push(BlkioStatsEntry {
            major: s.major as u64,
            minor: s.minor as u64,
            op: "Async".to_string(),
            value: s.r#async,
            unknown_fields: UnknownFields::default(),
            cached_size: CachedSize::default(),
        });
    }
    m
}

fn get_blkio_stats(dir: &str, relative_paths: &HashMap<String, String>) -> SingularPtrField<BlkioStats> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());
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

fn get_hugetlb_stats(dir: &str, relative_paths: &HashMap<String, String>) -> HashMap<String, HugetlbStats> {
    let h = cgroups::hierarchies::auto();
    let h = Box::new(&*h);
    let cg = load_or_create(h, dir, relative_paths.clone());

    let mut h = HashMap::new();

    let hugetlb_controller: Option<&HugeTlbController> = cg.controller_of();
    if hugetlb_controller.is_none() {
        return h;
    }
    let hugetlb_controller = hugetlb_controller.unwrap();

    let sizes = hugetlb_controller.get_sizes();
    for size in sizes {
        let usage = hugetlb_controller.usage_in_bytes(&size).unwrap_or(0);
        let max_usage = hugetlb_controller.max_usage_in_bytes(&size).unwrap_or(0);
        let failcnt = hugetlb_controller.failcnt(&size).unwrap_or(0);

        h.insert(
            size.to_string(),
            HugetlbStats {
                usage,
                max_usage,
                failcnt,
                unknown_fields: UnknownFields::default(),
                cached_size: CachedSize::default(),
            },
        );
    }

    h
}

pub const PATHS: &'static str = "/proc/self/cgroup";
pub const MOUNTS: &'static str = "/proc/self/mountinfo";

pub fn get_paths() -> Result<HashMap<String, String>> {
    let mut m = HashMap::new();
    for l in fs::read_to_string(PATHS)?.lines() {
        let fl: Vec<&str> = l.split(':').collect();
        if fl.len() != 3 {
            info!(sl!(), "Corrupted cgroup data!");
            continue;
        }

        let keys: Vec<&str> = fl[1].split(',').collect();
        for key in &keys {
            // this is a workaround, cgroup file are using `name=systemd`,
            // but if file system the name is `systemd`
            if *key == "name=systemd" {
                m.insert("systemd".to_string(), fl[2].to_string());
            } else {
                m.insert(key.to_string(), fl[2].to_string());
            }
        }
    }
    Ok(m)
}

pub fn get_mounts() -> Result<HashMap<String, String>> {
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
        if cpuset_cpus == "" {
            return Ok(());
        }

        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let root_cg = load_or_create(h, "", self.rels.clone());
        info!(sl!(), "update_cpuset_path to: {}", cpuset_cpus);

        let root_cpuset_controller: &CpuSetController = root_cg.controller_of().unwrap();
        let path = root_cpuset_controller.path();
        let root_path = Path::new(path);
        info!(sl!(), "root cpuset path: {:?}", &path);

        let h = cgroups::hierarchies::auto();
        let h = Box::new(&*h);
        let cg = load_or_create(h, &self.cpath, self.rels.clone());
        let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();
        let path = cpuset_controller.path();
        let container_path = Path::new(path);
        info!(sl!(), "container cpuset path: {:?}", &path);

        let mut paths = vec![];
        for ancestor in container_path.ancestors() {
            if ancestor == root_path {
                break;
            }
            if ancestor != container_path {
                paths.push(ancestor);
            }
        }
        info!(sl!(), "paths to update cpuset: {:?}", &paths);

        let mut i = paths.len();
        loop {
            if i == 0 {
                break;
            }
            i = i - 1;
            let h = cgroups::hierarchies::auto();
            let h = Box::new(&*h);
            // FIXME
            let r_path= &paths[i].to_str().unwrap().replace("/sys/fs/cgroup/cpuset","");
            let cg = load_or_create(h, &r_path, self.rels.clone());
            let cpuset_controller: &CpuSetController = cg.controller_of().unwrap();
            cpuset_controller.set_cpus(cpuset_cpus);
        }

        Ok(())
    }

    pub fn get_cg_path(&self, cg: &str) -> Option<String> {

        if cgroups::hierarchies::is_cgroup2_unified_mode() {
            let cg_path = format!("/sys/fs/cgroup/{}", self.cpath);
            return Some(cg_path);
        }

        // v1
        self.paths.get(cg).map(|s| s.to_string())
    }
}

pub fn get_guest_cpuset() -> Result<String> {
    let m = get_mounts()?;

    if m.get("cpuset").is_none() {
        warn!(sl!(), "no cpuset cgroup!");
        return Err(nix::Error::Sys(Errno::ENOENT).into());
    }
    let p = format!("{}/cpuset.cpus", m.get("cpuset").unwrap());
    let c = fs::read_to_string(p.as_str())?;
    Ok(c)
}

// Since the OCI spec is designed for cgroup v1, in some cases
// there is need to convert from the cgroup v1 configuration to cgroup v2
// the formula for cpuShares is y = (1 + ((x - 2) * 9999) / 262142)
// convert from [2-262144] to [1-10000]
// 262144 comes from Linux kernel definition "#define MAX_SHARES (1UL << 18)"
// from https://github.com/opencontainers/runc/blob/a5847db387ae28c0ca4ebe4beee1a76900c86414/libcontainer/cgroups/utils.go#L394
pub fn convert_shares_to_v2_value(shares:u64) -> u64 {
	if shares == 0 {
		return 0
	}
	1 + ((shares-2)*9999)/262142
}


// ConvertMemorySwapToCgroupV2Value converts MemorySwap value from OCI spec
// for use by cgroup v2 drivers. A conversion is needed since Resources.MemorySwap
// is defined as memory+swap combined, while in cgroup v2 swap is a separate value.
fn convert_memory_swap_to_v2_value(memory_swap: i64, memory: i64) -> Result<i64> {
    // for compatibility with cgroup1 controller, set swap to unlimited in
    // case the memory is set to unlimited, and swap is not explicitly set,
    // treating the request as "set both memory and swap to unlimited".
    if memory == -1 && memory_swap == 0 {
        return Ok(-1);
    }
    if memory_swap == -1 || memory_swap == 0 {
        // -1 is "max", 0 is "unset", so treat as is
        return Ok(memory_swap);
    }
    // sanity checks
    if memory == 0 || memory == -1 {
        return Err(ErrorKind::ErrorCode("unable to set swap limit without memory limit".to_string()).into());
    }
    if memory < 0 {
        return Err(ErrorKind::ErrorCode(format!("invalid memory value: {}", memory)).into());
    }
    if memory_swap < memory {
        return Err(ErrorKind::ErrorCode("memory+swap limit should be >= memory limit".to_string()).into());
    }
    Ok(memory_swap - memory)
}