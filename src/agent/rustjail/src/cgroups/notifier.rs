use crate::errors::*;

use eventfd::{eventfd, EfdFlags};
use nix::sys::eventfd;
use std::fs::{self, File};
use std::io::Read;
use std::os::unix::io::{AsRawFd, FromRawFd};
use std::path::Path;
use std::sync::mpsc::{self, Receiver, Sender};
use std::thread;

// Convenience macro to obtain the scope logger
macro_rules! sl {
    () => {
        slog_scope::logger().new(o!("subsystem" => "cgroups_notifier"))
    };
}

pub fn notify_oom(cid: &str, path: &str) -> Result<Receiver<String>> {
    // if c.config.RootlessCgroups {
    // 	logrus.Warn("getting OOM notifications may fail if you don't have the full access to cgroups")
    // }
    // path := c.cgroupManager.Path("memory")
    // if cgroups.IsCgroup2UnifiedMode() {
    // 	return notifyOnOOMV2(path)
    // }
    notify_on_oom(cid, path)
}

// notify_on_oom returns channel on which you can expect event about OOM,
// if process died without OOM this channel will be closed.
fn notify_on_oom(cid: &str, dir: &str) -> Result<Receiver<String>> {
    if dir == "" {
        return Err(ErrorKind::ErrorCode("memory controller missing".to_string()).into());
    }

    register_memory_event(cid, dir, "memory.oom_control", "")
}

// level is one of "low", "medium", or "critical"
fn notify_memory_pressure(cid: &str, dir: &str, level: &str) -> Result<Receiver<String>> {
    if dir == "" {
        return Err(ErrorKind::ErrorCode("memory controller missing".to_string()).into());
    }

    if level != "low" && level != "medium" && level != "critical" {
        return Err(ErrorKind::ErrorCode(format!("invalid pressure level {}", level)).into());
    }

    register_memory_event(cid, dir, "memory.pressure_level", level)
}

fn register_memory_event(
    cid: &str,
    cg_dir: &str,
    event_name: &str,
    arg: &str,
) -> Result<Receiver<String>> {
    let path = Path::new(cg_dir).join(event_name);
    let event_file = File::open(path)?;

    let eventfd = eventfd(0, EfdFlags::EFD_CLOEXEC)?;

    let event_control_path = Path::new(cg_dir).join("cgroup.event_control");
    let data = format!("{} {} {}", eventfd, event_file.as_raw_fd(), arg);

    // write to file and set mode to 0700(FIXME)
    fs::write(&event_control_path, data)?;

    let mut event_file = unsafe { File::from_raw_fd(eventfd) };

    let (sender, receiver) = mpsc::channel();
    let containere_id = cid.to_string();

    thread::spawn(move || {
        loop {
            let mut buf = [0; 8];
            match event_file.read(&mut buf) {
                Err(err) => {
                    warn!(sl!(), "failed to read from eventfd: {:?}", err);
                    return;
                }
                Ok(_) => {}
            }

            // When a cgroup is destroyed, an event is sent to eventfd.
            // So if the control path is gone, return instead of notifying.
            if !Path::new(&event_control_path).exists() {
                return;
            }
            sender.send(containere_id.clone()).unwrap();
        }
    });

    Ok(receiver)
}
