// Copyright (c) 2019,2020 Ant Financial
//
// SPDX-License-Identifier: Apache-2.0
//

use crate::errors::*;
use oci::LinuxResources;
use protocols::agent::CgroupStats;
use std::collections::HashMap;

use cgroups::freezer::FreezerState;

pub mod fs;
pub mod notifier;
pub mod systemd;

pub trait Manager {
    fn apply(&self, _pid: i32) -> Result<()> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }

    fn get_pids(&self) -> Result<Vec<i32>> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }

    fn get_stats(&self) -> Result<CgroupStats> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }

    fn freeze(&self, _state: FreezerState) -> Result<()> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }

    fn destroy(&mut self) -> Result<()> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }

    fn set(&self, _container: &LinuxResources, _update: bool) -> Result<()> {
        Err(ErrorKind::ErrorCode("not supported!".to_string()).into())
    }
}
