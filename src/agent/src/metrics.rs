extern crate procfs;

use prometheus::{Encoder, Gauge, GaugeVec, IntCounter, TextEncoder};

use protocols;
use rustjail::errors::*;

const NAMESPACE_KATA_AGENT: &str = "kata_agent";
const NAMESPACE_KATA_GUEST: &str = "kata_guest";

// Convenience macro to obtain the scope logger
macro_rules! sl {
    () => {
        slog_scope::logger().new(o!("subsystem" => "metrics"))
    };
}

lazy_static! {

    static ref     AGENT_SCRAPE_COUNT: IntCounter =
    prometheus::register_int_counter!(format!("{}_{}",NAMESPACE_KATA_AGENT,"scrape_count").as_ref(), "Metrics scrape count").unwrap();

    static ref     AGENT_THREADS: Gauge =
    prometheus::register_gauge!(format!("{}_{}",NAMESPACE_KATA_AGENT,"threads").as_ref(), "Agent process threads").unwrap();

    static ref     AGENT_TOTAL_TIME: Gauge =
    prometheus::register_gauge!(format!("{}_{}",NAMESPACE_KATA_AGENT,"total_time").as_ref(), "Agent process total time").unwrap();

    static ref     AGENT_TOTAL_VM: Gauge =
    prometheus::register_gauge!(format!("{}_{}",NAMESPACE_KATA_AGENT,"total_vm").as_ref(), "Agent process total vm size").unwrap();

    static ref     AGENT_TOTAL_RSS: Gauge =
    prometheus::register_gauge!(format!("{}_{}",NAMESPACE_KATA_AGENT,"total_rss").as_ref(), "Agent process total rss size").unwrap();

    static ref     AGENT_PROC_STATUS: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_AGENT,"proc_status").as_ref(), "Agent proc status.", &["item"]).unwrap();

    static ref     AGENT_IO_STAT: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_AGENT,"io_stat").as_ref(), "Agent process IO stat.", &["item"]).unwrap();

    static ref     AGENT_PROC_STAT: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_AGENT,"proc_stat").as_ref(), "Agent process stat.", &["item"]).unwrap();

    // guest os metrics
    static ref     GUEST_LOAD: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"load").as_ref() , "Guest system load.", &["item"]).unwrap();

    static ref     GUEST_TASKS: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"tasks").as_ref() , "Guest system load.", &["item"]).unwrap();

    static ref     GUEST_CPU_TIME: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"cpu_time").as_ref() , "Guest CPU stat.", &["cpu","item"]).unwrap();

    static ref     GUEST_VM_STAT: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"vm_stat").as_ref() , "Guest virtual memory stat.", &["item"]).unwrap();

    static ref     GUEST_NETDEV_STAT: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"netdev_stat").as_ref() , "Guest net devices stats.", &["interface","item"]).unwrap();

    static ref     DISKSTAT: GaugeVec =
    prometheus::register_gauge_vec!(format!("{}_{}",NAMESPACE_KATA_GUEST,"diskstat").as_ref() , "Disks stat in system.", &["disk"]).unwrap();
}

pub fn get_metrics(_: &protocols::agent::GetMetricsRequest) -> Result<String> {
    AGENT_SCRAPE_COUNT.inc();

    // update agent process metrics
    update_agent_metrics();

    // update guest os metrics
    update_guest_metrics();

    // gather all metrics and return as a String
    let metric_families = prometheus::gather();

    let mut buffer = Vec::new();
    let encoder = TextEncoder::new();
    encoder.encode(&metric_families, &mut buffer).unwrap();

    Ok(String::from_utf8(buffer.clone()).unwrap())
}

fn update_agent_metrics() {
    let me = procfs::process::Process::myself().unwrap();

    let tps = procfs::ticks_per_second().unwrap();

    // process total time
    AGENT_TOTAL_TIME.set((me.stat.utime + me.stat.stime) as f64 / (tps as f64));

    // Total virtual memory used
    AGENT_TOTAL_VM.set(me.stat.vsize as f64);

    // Total resident set
    let page_size = procfs::page_size().unwrap() as f64;
    AGENT_TOTAL_RSS.set(me.stat.rss as f64 * page_size);

    // io
    match me.io() {
        Err(err) => {
            info!(sl!(), "failed to get process io stat: {:?}", err);
        }
        Ok(io) => {
            set_gauge_vec_proc_io(&AGENT_IO_STAT, &io);
        }
    }

    match me.stat() {
        Err(err) => {
            info!(sl!(), "failed to get process stat: {:?}", err);
        }
        Ok(stat) => {
            set_gauge_vec_proc_stat(&AGENT_PROC_STAT, &stat);
        }
    }

    match me.status() {
        Err(err) => {
            info!(sl!(), "failed to get process status: {:?}", err);
        }
        Ok(status) => set_gauge_vec_proc_status(&AGENT_PROC_STATUS, &status),
    }
}

fn update_guest_metrics() {
    // try get load and task info
    match procfs::LoadAverage::new() {
        Err(err) => {
            info!(sl!(), "failed to get guest LoadAverage: {:?}", err);
        }
        Ok(load) => {
            GUEST_LOAD
                .with_label_values(&["load1"])
                .set(load.one as f64);
            GUEST_LOAD
                .with_label_values(&["load5"])
                .set(load.five as f64);
            GUEST_LOAD
                .with_label_values(&["load15"])
                .set(load.fifteen as f64);
            GUEST_TASKS.with_label_values(&["cur"]).set(load.cur as f64);
            GUEST_TASKS.with_label_values(&["max"]).set(load.max as f64);
        }
    }

    // try to get disk stats
    match procfs::diskstats() {
        Err(err) => {
            info!(sl!(), "failed to get guest diskstats: {:?}", err);
        }
        Ok(diskstats) => {
            for diskstat in diskstats {
                set_gauge_vec_diskstat(&DISKSTAT, &diskstat);
            }
        }
    }

    // try to get vm stats
    match procfs::vmstat() {
        Err(err) => {
            info!(sl!(), "failed to get guest vmstat: {:?}", err);
        }
        Ok(vmstat) => {
            for (k, v) in vmstat {
                GUEST_VM_STAT.with_label_values(&[k.as_str()]).set(v as f64);
            }
        }
    }

    // cpu stat
    match procfs::KernelStats::new() {
        Err(err) => {
            info!(sl!(), "failed to get guest KernelStats: {:?}", err);
        }
        Ok(kernel_stats) => {
            set_gauge_vec_CPU_time(&GUEST_CPU_TIME, "total", &kernel_stats.total);
            for (i, cpu_time) in kernel_stats.cpu_time.iter().enumerate() {
                set_gauge_vec_CPU_time(&GUEST_CPU_TIME, format!("{}", i).as_str(), &cpu_time);
            }
        }
    }

    // try to get net device stats
    match procfs::net::dev_status() {
        Err(err) => {
            info!(sl!(), "failed to get guest net::dev_status: {:?}", err);
        }
        Ok(devs) => {
            // netdev: map[string]procfs::net::DeviceStatus
            for (_, status) in devs {
                set_gauge_vec_netdev(&GUEST_NETDEV_STAT, &status);
            }
        }
    }
}

fn set_gauge_vec_CPU_time(gv: &prometheus::GaugeVec, cpu: &str, cpu_time: &procfs::CpuTime) {
    gv.with_label_values(&[cpu, "user"])
        .set(cpu_time.user as f64);
    gv.with_label_values(&[cpu, "nice"])
        .set(cpu_time.nice as f64);
    gv.with_label_values(&[cpu, "system"])
        .set(cpu_time.system as f64);
    gv.with_label_values(&[cpu, "idle"])
        .set(cpu_time.idle as f64);
    gv.with_label_values(&[cpu, "iowait"])
        .set(cpu_time.iowait.unwrap_or(0.0) as f64);
    gv.with_label_values(&[cpu, "irq"])
        .set(cpu_time.irq.unwrap_or(0.0) as f64);
    gv.with_label_values(&[cpu, "softirq"])
        .set(cpu_time.softirq.unwrap_or(0.0) as f64);
    gv.with_label_values(&[cpu, "steal"])
        .set(cpu_time.steal.unwrap_or(0.0) as f64);
    gv.with_label_values(&[cpu, "guest"])
        .set(cpu_time.guest.unwrap_or(0.0) as f64);
    gv.with_label_values(&[cpu, "guest_nice"])
        .set(cpu_time.guest_nice.unwrap_or(0.0) as f64);
}

fn set_gauge_vec_diskstat(gv: &prometheus::GaugeVec, diskstat: &procfs::DiskStat) {
    gv.with_label_values(&[diskstat.name.as_str(), "reads"])
        .set(diskstat.reads as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "merged"])
        .set(diskstat.merged as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "sectors_read"])
        .set(diskstat.sectors_read as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "time_reading"])
        .set(diskstat.time_reading as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "writes"])
        .set(diskstat.writes as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "writes_merged"])
        .set(diskstat.writes_merged as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "sectors_written"])
        .set(diskstat.sectors_written as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "time_writing"])
        .set(diskstat.time_writing as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "in_progress"])
        .set(diskstat.in_progress as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "time_in_progress"])
        .set(diskstat.time_in_progress as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "weighted_time_in_progress"])
        .set(diskstat.weighted_time_in_progress as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "discards"])
        .set(diskstat.discards.unwrap_or(0) as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "discards_merged"])
        .set(diskstat.discards_merged.unwrap_or(0) as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "sectors_discarded"])
        .set(diskstat.sectors_discarded.unwrap_or(0) as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "time_discarding"])
        .set(diskstat.time_discarding.unwrap_or(0) as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "flushes"])
        .set(diskstat.flushes.unwrap_or(0) as f64);
    gv.with_label_values(&[diskstat.name.as_str(), "time_flushing"])
        .set(diskstat.time_flushing.unwrap_or(0) as f64);
}

// set_gauge_vec_netdev set gauge for NetDevLine
fn set_gauge_vec_netdev(gv: &prometheus::GaugeVec, status: &procfs::net::DeviceStatus) {
    gv.with_label_values(&[status.name.as_str(), "recv_bytes"])
        .set(status.recv_bytes as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_packets"])
        .set(status.recv_packets as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_errs"])
        .set(status.recv_errs as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_drop"])
        .set(status.recv_drop as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_fifo"])
        .set(status.recv_fifo as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_frame"])
        .set(status.recv_frame as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_compressed"])
        .set(status.recv_compressed as f64);
    gv.with_label_values(&[status.name.as_str(), "recv_multicast"])
        .set(status.recv_multicast as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_bytes"])
        .set(status.sent_bytes as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_packets"])
        .set(status.sent_packets as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_errs"])
        .set(status.sent_errs as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_drop"])
        .set(status.sent_drop as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_fifo"])
        .set(status.sent_fifo as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_colls"])
        .set(status.sent_colls as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_carrier"])
        .set(status.sent_carrier as f64);
    gv.with_label_values(&[status.name.as_str(), "sent_compressed"])
        .set(status.sent_compressed as f64);
}

// set_gauge_vec_proc_status set gauge for ProcStatus
fn set_gauge_vec_proc_status(gv: &prometheus::GaugeVec, status: &procfs::process::Status) {
    gv.with_label_values(&["vmpeak"])
        .set(status.vmpeak.unwrap_or(0) as f64);
    gv.with_label_values(&["vmsize"])
        .set(status.vmsize.unwrap_or(0) as f64);
    gv.with_label_values(&["vmlck"])
        .set(status.vmlck.unwrap_or(0) as f64);
    gv.with_label_values(&["vmpin"])
        .set(status.vmpin.unwrap_or(0) as f64);
    gv.with_label_values(&["vmhwm"])
        .set(status.vmhwm.unwrap_or(0) as f64);
    gv.with_label_values(&["vmrss"])
        .set(status.vmrss.unwrap_or(0) as f64);
    gv.with_label_values(&["rssanon"])
        .set(status.rssanon.unwrap_or(0) as f64);
    gv.with_label_values(&["rssfile"])
        .set(status.rssfile.unwrap_or(0) as f64);
    gv.with_label_values(&["rssshmem"])
        .set(status.rssshmem.unwrap_or(0) as f64);
    gv.with_label_values(&["vmdata"])
        .set(status.vmdata.unwrap_or(0) as f64);
    gv.with_label_values(&["vmstk"])
        .set(status.vmstk.unwrap_or(0) as f64);
    gv.with_label_values(&["vmexe"])
        .set(status.vmexe.unwrap_or(0) as f64);
    gv.with_label_values(&["vmlib"])
        .set(status.vmlib.unwrap_or(0) as f64);
    gv.with_label_values(&["vmpte"])
        .set(status.vmpte.unwrap_or(0) as f64);
    gv.with_label_values(&["vmswap"])
        .set(status.vmswap.unwrap_or(0) as f64);
    gv.with_label_values(&["hugetblpages"])
        .set(status.hugetblpages.unwrap_or(0) as f64);
}

// set_gauge_vec_proc_io set gauge for ProcIO
fn set_gauge_vec_proc_io(gv: &prometheus::GaugeVec, io_stat: &procfs::process::Io) {
    gv.with_label_values(&["rchar"]).set(io_stat.rchar as f64);
    gv.with_label_values(&["wchar"]).set(io_stat.wchar as f64);
    gv.with_label_values(&["syscr"]).set(io_stat.syscr as f64);
    gv.with_label_values(&["syscw"]).set(io_stat.syscw as f64);
    gv.with_label_values(&["read_bytes"])
        .set(io_stat.read_bytes as f64);
    gv.with_label_values(&["write_bytes"])
        .set(io_stat.write_bytes as f64);
    gv.with_label_values(&["cancelled_write_bytes]"])
        .set(io_stat.cancelled_write_bytes as f64);
}

// set_gauge_vec_proc_stat set gauge for ProcStat
fn set_gauge_vec_proc_stat(gv: &prometheus::GaugeVec, stat: &procfs::process::Stat) {
    gv.with_label_values(&["utime"]).set(stat.utime as f64);
    gv.with_label_values(&["stime"]).set(stat.stime as f64);
    gv.with_label_values(&["cutime"]).set(stat.cutime as f64);
    gv.with_label_values(&["cstime"]).set(stat.cstime as f64);
}
