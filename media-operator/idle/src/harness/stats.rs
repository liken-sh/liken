// The measurements of a run (`--stats`) and the JSON report written at
// exit.

use std::path::PathBuf;
use std::time::Instant;

use serde_json::json;

pub struct Stats {
    /// Where the report goes at exit, from `--stats`.
    path: PathBuf,
    /// When the process started, to measure the time to the first
    /// frame.
    launched: Instant,
    backend: String,
    adapter: String,
    size: (u32, u32),
    frames: u64,
    first_frame: Option<f64>,
    /// Milliseconds to build, draw, and submit each counted frame.
    build_ms: Vec<f64>,
    /// Milliseconds for each counted pass of the loop, which includes
    /// the wait for the next swapchain image. When the compositor paces
    /// frames to the display, this is the frame interval, and
    /// `build_ms` is the work within it.
    loop_ms: Vec<f64>,
    rss_mib: Vec<f64>,
    next_rss_at: f64,
}

impl Stats {
    /// Returns `None` when `--stats` named no file, so a run without the
    /// flag keeps no samples.
    pub fn requested(
        path: Option<PathBuf>,
        launched: Instant,
        backend: String,
        adapter: String,
        size: (u32, u32),
    ) -> Option<Self> {
        Some(Self {
            path: path?,
            launched,
            backend,
            adapter,
            size,
            frames: 0,
            first_frame: None,
            build_ms: Vec::new(),
            loop_ms: Vec::new(),
            rss_mib: Vec::new(),
            next_rss_at: 0.0,
        })
    }

    /// Records a new surface size and discards the frame times
    /// collected so far. Those times were measured at the old size, and
    /// the frames around a resize include the cost of creating a new
    /// swapchain.
    pub fn resized(&mut self, size: (u32, u32)) {
        self.size = size;
        self.build_ms.clear();
        self.loop_ms.clear();
    }

    /// Records the time of the first frame, measured from process
    /// start, so it includes the wgpu setup, the first window, and the
    /// first draw.
    pub fn first_frame(&mut self) {
        self.first_frame = Some(self.launched.elapsed().as_secs_f64());
    }

    /// Records one frame. When `counted` is false, the frame counts
    /// toward `frames` and its times are not recorded.
    pub fn frame(&mut self, build_ms: f64, loop_ms: f64, counted: bool) {
        self.frames += 1;
        if counted {
            self.build_ms.push(build_ms);
            self.loop_ms.push(loop_ms);
        }
    }

    /// Records the process's resident set size, at most once a second.
    /// The value is the `VmRSS` line of `/proc/self/status`.
    pub fn sample_rss(&mut self, at: f64) {
        if at < self.next_rss_at {
            return;
        }
        self.next_rss_at = at.floor() + 1.0;
        if let Some(mib) = read_rss_mib() {
            self.rss_mib.push(mib);
        }
    }

    pub fn report(&self) -> serde_json::Value {
        json!({
            "backend": self.backend,
            "adapter": self.adapter,
            "width": self.size.0,
            "height": self.size.1,
            "frames": self.frames,
            "seconds_to_first_frame": rounded(self.first_frame.unwrap_or(f64::NAN), 4),
            "frame_ms_p50": rounded(percentile(&self.build_ms, 0.50), 3),
            "frame_ms_p99": rounded(percentile(&self.build_ms, 0.99), 3),
            "frame_ms_max": rounded(percentile(&self.build_ms, 1.0), 3),
            "loop_ms_p50": rounded(percentile(&self.loop_ms, 0.50), 3),
            "loop_ms_p99": rounded(percentile(&self.loop_ms, 0.99), 3),
            "rss_mib": self.rss_mib.iter().map(|value| rounded(*value, 1)).collect::<Vec<_>>(),
            "rss_mib_max": rounded(self.rss_mib.iter().copied().fold(0.0_f64, f64::max), 1),
        })
    }

    /// Writes the report to the `--stats` file and prints an error if
    /// that fails.
    pub fn write(&self) {
        let json = format!("{:#}\n", self.report());
        if let Err(error) = std::fs::write(&self.path, json) {
            eprintln!("writing the stats file {}: {error}", self.path.display());
        }
    }
}

pub fn millis(elapsed: std::time::Duration) -> f64 {
    elapsed.as_secs_f64() * 1000.0
}

/// The nearest-rank percentile of `values`, or NaN when there are none.
/// It sorts a copy, which is acceptable because it runs once at exit
/// and never on the frame path.
pub fn percentile(values: &[f64], fraction: f64) -> f64 {
    if values.is_empty() {
        return f64::NAN;
    }
    let mut sorted = values.to_vec();
    sorted.sort_by(|a, b| a.total_cmp(b));
    let rank = ((sorted.len() as f64 - 1.0) * fraction).round() as usize;
    sorted[rank]
}

/// Rounds `value` to `places` decimal places. A NaN becomes JSON
/// `null`, which is how `serde_json` serializes it.
fn rounded(value: f64, places: u32) -> serde_json::Value {
    let scale = 10_f64.powi(places as i32);
    json!((value * scale).round() / scale)
}

fn read_rss_mib() -> Option<f64> {
    let status = std::fs::read_to_string("/proc/self/status").ok()?;
    let line = status.lines().find(|line| line.starts_with("VmRSS:"))?;
    let kib: f64 = line.split_whitespace().nth(1)?.parse().ok()?;
    Some(kib / 1024.0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_duration_reads_as_milliseconds() {
        assert_eq!(millis(std::time::Duration::from_micros(1500)), 1.5);
    }

    #[test]
    fn a_percentile_is_the_nearest_rank() {
        let sample: Vec<f64> = (1..=101).map(f64::from).collect();
        assert_eq!(percentile(&sample, 0.0), 1.0);
        assert_eq!(percentile(&sample, 0.50), 51.0);
        assert_eq!(percentile(&sample, 0.99), 100.0);
        assert_eq!(percentile(&sample, 1.0), 101.0);
    }

    #[test]
    fn a_percentile_is_taken_from_the_sorted_values() {
        assert_eq!(percentile(&[9.0, 1.0, 5.0], 0.50), 5.0);
        assert_eq!(percentile(&[9.0, 1.0, 5.0], 1.0), 9.0);
    }

    #[test]
    fn one_value_is_every_percentile() {
        assert_eq!(percentile(&[4.0], 0.50), 4.0);
        assert_eq!(percentile(&[4.0], 0.99), 4.0);
    }

    #[test]
    fn an_empty_sample_has_no_percentile() {
        assert!(
            percentile(&[], 0.50).is_nan(),
            "the percentile of no samples is a number, want NaN"
        );
    }

    fn collector() -> Stats {
        Stats::requested(
            Some(PathBuf::from("/stats.json")),
            Instant::now(),
            "Vulkan".into(),
            "an adapter".into(),
            (1920, 1080),
        )
        .expect("a run that names a file measures")
    }

    #[test]
    fn a_run_with_no_file_measures_nothing() {
        assert!(
            Stats::requested(
                None,
                Instant::now(),
                "Vulkan".into(),
                "an adapter".into(),
                (1920, 1080),
            )
            .is_none(),
            "a run with no file measures, want no Stats"
        );
    }

    fn measured() -> Stats {
        let mut stats = collector();
        stats.first_frame();
        for step in 1..=10 {
            stats.frame(f64::from(step), 16.0, true);
        }
        stats.frame(40.0, 40.0, false);
        stats
    }

    #[test]
    fn the_report_counts_every_frame_and_times_only_the_counted_ones() {
        let report = measured().report();
        assert_eq!(report["frames"], json!(11));
        assert_eq!(report["frame_ms_max"], json!(10.0));
        assert_eq!(report["loop_ms_p50"], json!(16.0));
    }

    #[test]
    fn the_time_to_the_first_frame_is_measured_from_the_launch() {
        let mut stats = collector();
        assert_eq!(stats.report()["seconds_to_first_frame"], json!(null));

        stats.first_frame();

        let seconds = stats.report()["seconds_to_first_frame"].as_f64();
        assert!(
            seconds.is_some_and(|seconds| seconds >= 0.0),
            "seconds_to_first_frame is {seconds:?}, want a number of at least 0"
        );
    }

    #[test]
    fn a_resize_discards_the_frame_times_before_it() {
        let mut stats = measured();
        stats.resized((1280, 720));
        let report = stats.report();
        assert_eq!(report["width"], json!(1280));
        assert_eq!(report["frames"], json!(11));
        assert_eq!(report["frame_ms_p50"], json!(null));
    }

    #[test]
    fn a_resident_set_sample_is_taken_once_a_second() {
        let mut stats = collector();
        for step in 0..30 {
            stats.sample_rss(f64::from(step) * 0.1);
        }
        let samples = stats.report()["rss_mib"].as_array().unwrap().len();
        assert_eq!(samples, 3);
    }
}
