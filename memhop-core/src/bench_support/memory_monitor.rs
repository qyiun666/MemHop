//! 内存监控工具 — 跨平台测量进程内存使用。

use std::time::Instant;

/// 内存快照。
#[derive(Debug, Clone)]
pub struct MemorySnapshot {
    /// RSS (Resident Set Size) 字节
    pub rss_bytes: u64,
    /// VSZ (Virtual Size) 字节
    pub vm_bytes: u64,
    /// 快照时间戳
    pub timestamp: Instant,
}

/// 内存统计差异。
#[derive(Debug)]
pub struct MemoryDelta {
    pub rss_delta_bytes: i64,
    pub vm_delta_bytes: i64,
    pub elapsed: std::time::Duration,
}

/// 内存泄漏检测结果。
#[derive(Debug)]
pub struct LeakCheckResult {
    /// 初始 RSS
    pub initial_rss: u64,
    /// 最终 RSS
    pub final_rss: u64,
    /// 增长量（字节）
    pub growth_bytes: i64,
    /// 是否通过（增长 < threshold_mb）
    pub passed: bool,
    /// 阈值（MB）
    pub threshold_mb: u64,
}

/// 内存监控器。
pub struct MemoryMonitor {
    start_time: Instant,
}

impl MemoryMonitor {
    /// 创建新的内存监控器。
    pub fn new() -> Self {
        Self {
            start_time: Instant::now(),
        }
    }

    /// 获取当前内存快照。
    pub fn snapshot(&self) -> MemorySnapshot {
        let (rss, vm) = get_process_memory();
        MemorySnapshot {
            rss_bytes: rss,
            vm_bytes: vm,
            timestamp: Instant::now(),
        }
    }

    /// 计算两个快照之间的差异。
    pub fn delta(&self, before: &MemorySnapshot, after: &MemorySnapshot) -> MemoryDelta {
        MemoryDelta {
            rss_delta_bytes: after.rss_bytes as i64 - before.rss_bytes as i64,
            vm_delta_bytes: after.vm_bytes as i64 - before.vm_bytes as i64,
            elapsed: after.timestamp.duration_since(before.timestamp),
        }
    }

    /// 泄漏检测：比较初始和最终内存，检查增长是否在阈值内。
    pub fn leak_check(
        &self,
        initial: &MemorySnapshot,
        final_snap: &MemorySnapshot,
        threshold_mb: u64,
    ) -> LeakCheckResult {
        let growth = final_snap.rss_bytes as i64 - initial.rss_bytes as i64;
        let threshold_bytes = threshold_mb * 1024 * 1024;
        LeakCheckResult {
            initial_rss: initial.rss_bytes,
            final_rss: final_snap.rss_bytes,
            growth_bytes: growth,
            passed: (growth.unsigned_abs()) < threshold_bytes,
            threshold_mb,
        }
    }

    /// 获取监控器已运行时间。
    pub fn elapsed(&self) -> std::time::Duration {
        self.start_time.elapsed()
    }
}

impl Default for MemoryMonitor {
    fn default() -> Self {
        Self::new()
    }
}

/// 获取当前进程的内存使用 (RSS, VSZ) 字节。
/// 跨平台支持：macOS 使用 ps，Linux 使用 /proc/self/status。
fn get_process_memory() -> (u64, u64) {
    #[cfg(target_os = "macos")]
    {
        get_memory_macos()
    }
    #[cfg(target_os = "linux")]
    {
        get_memory_linux()
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux")))]
    {
        (0, 0)
    }
}

#[cfg(target_os = "macos")]
fn get_memory_macos() -> (u64, u64) {
    use std::process::Command;
    let pid = std::process::id();
    let output = Command::new("ps")
        .args(["-o", "rss=,vsz=", "-p", &pid.to_string()])
        .output();
    match output {
        Ok(out) => {
            let stdout = String::from_utf8_lossy(&out.stdout);
            let parts: Vec<&str> = stdout.split_whitespace().collect();
            if parts.len() >= 2 {
                let rss_kb: u64 = parts[0].parse().unwrap_or(0);
                let vsz_kb: u64 = parts[1].parse().unwrap_or(0);
                (rss_kb * 1024, vsz_kb * 1024)
            } else {
                (0, 0)
            }
        }
        Err(_) => (0, 0),
    }
}

#[cfg(target_os = "linux")]
fn get_memory_linux() -> (u64, u64) {
    use std::fs;
    if let Ok(content) = fs::read_to_string("/proc/self/status") {
        let mut rss_kb = 0u64;
        let mut vsz_kb = 0u64;
        for line in content.lines() {
            if let Some(val) = line.strip_prefix("VmRSS:") {
                rss_kb = val.trim().trim_end_matches(" kB").parse().unwrap_or(0);
            }
            if let Some(val) = line.strip_prefix("VmSize:") {
                vsz_kb = val.trim().trim_end_matches(" kB").parse().unwrap_or(0);
            }
        }
        (rss_kb * 1024, vsz_kb * 1024)
    } else {
        (0, 0)
    }
}

/// 将字节格式化为人类可读的字符串。
pub fn format_bytes(bytes: u64) -> String {
    if bytes < 1024 {
        format!("{} B", bytes)
    } else if bytes < 1024 * 1024 {
        format!("{:.1} KB", bytes as f64 / 1024.0)
    } else if bytes < 1024 * 1024 * 1024 {
        format!("{:.1} MB", bytes as f64 / (1024.0 * 1024.0))
    } else {
        format!("{:.2} GB", bytes as f64 / (1024.0 * 1024.0 * 1024.0))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_memory_monitor_snapshot() {
        let monitor = MemoryMonitor::new();
        let snap = monitor.snapshot();
        // 基本检查：进程至少有 1MB 的 RSS
        assert!(snap.rss_bytes > 1024 * 1024, "RSS should be > 1MB");
    }

    #[test]
    fn test_format_bytes() {
        assert_eq!(format_bytes(512), "512 B");
        assert_eq!(format_bytes(1024), "1.0 KB");
        assert_eq!(format_bytes(1024 * 1024), "1.0 MB");
        assert_eq!(format_bytes(1024 * 1024 * 1024), "1.00 GB");
    }
}
