// The frames that a run saves as PNG files (`--capture` and
// `--capture-at`). `Captures::due` takes the time as an argument, so
// tests call it with numbers and open no window.

use std::path::{Path, PathBuf};

/// The output directory, the requested capture seconds, and the index
/// of the next one. The index only increases, so each capture happens
/// once.
#[derive(Debug)]
pub struct Captures {
    dir: PathBuf,
    at: Vec<f64>,
    next: usize,
}

impl Captures {
    /// Returns `None` when `--capture` named no directory, even when
    /// `--capture-at` lists seconds.
    pub fn requested(dir: Option<PathBuf>, at: Vec<f64>) -> Option<Self> {
        Some(Self {
            dir: dir?,
            at,
            next: 0,
        })
    }

    /// The file to write this frame to, if it is the first frame at or
    /// after the next capture second. The name is that second as
    /// `SSS.ss.png`.
    pub fn due(&mut self, at: f64) -> Option<PathBuf> {
        let when = *self.at.get(self.next)?;
        if at < when {
            return None;
        }
        self.next += 1;
        Some(self.dir.join(format!("{when:06.2}.png")))
    }

    /// The second of the next capture, which the loop includes when it
    /// picks its next wake time.
    pub fn next_due(&self) -> Option<f64> {
        self.at.get(self.next).copied()
    }

    /// Whether a capture is still scheduled.
    pub fn pending(&self) -> bool {
        self.next < self.at.len()
    }

    /// True when every requested capture is written. A run with no
    /// capture seconds returns false, so it does not end on its captures.
    pub fn taken(&self) -> bool {
        !self.at.is_empty() && !self.pending()
    }
}

/// Writes one frame as a PNG. `rgba` is the pixels that the renderer
/// read back. Failures are printed and do not stop the run.
pub fn write_png(path: &Path, width: u32, height: u32, rgba: &[u8]) {
    if let Some(dir) = path.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    let Some(buffer) = image::RgbaImage::from_raw(width, height, rgba.to_vec()) else {
        eprintln!(
            "writing the capture {}: {} bytes do not fill {width}x{height} RGBA pixels",
            path.display(),
            rgba.len()
        );
        return;
    };
    if let Err(error) = buffer.save(path) {
        eprintln!("writing the capture {}: {error}", path.display());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn capturing(dir: &str, at: Vec<f64>) -> Captures {
        Captures::requested(Some(PathBuf::from(dir)), at).expect("a directory captures")
    }

    #[test]
    fn a_run_with_no_directory_captures_nothing() {
        assert!(
            Captures::requested(None, vec![0.5]).is_none(),
            "a run with no directory captures, want no Captures"
        );
    }

    #[test]
    fn a_capture_is_due_on_the_first_frame_at_or_after_its_second() {
        let mut captures = capturing("/frames", vec![0.5]);
        assert_eq!(captures.due(0.49), None);
        assert_eq!(
            captures.due(0.52),
            Some(PathBuf::from("/frames/000.50.png"))
        );
        assert_eq!(captures.due(0.53), None);
    }

    #[test]
    fn a_capture_is_named_for_the_second_it_was_asked_for() {
        let mut captures = capturing("/frames", vec![12.25, 100.0]);
        assert_eq!(
            captures.due(20.0),
            Some(PathBuf::from("/frames/012.25.png"))
        );
        assert_eq!(
            captures.due(200.0),
            Some(PathBuf::from("/frames/100.00.png"))
        );
    }

    #[test]
    fn the_next_capture_is_due_until_it_is_taken() {
        let mut captures = capturing("/frames", vec![0.5, 1.5]);
        assert_eq!(captures.next_due(), Some(0.5));

        captures.due(0.5);
        assert_eq!(captures.next_due(), Some(1.5));

        captures.due(1.5);
        assert_eq!(captures.next_due(), None);
    }

    #[test]
    fn a_run_has_finished_its_captures_after_the_last_one() {
        let mut captures = capturing("/frames", vec![0.5, 1.5]);
        assert!(captures.pending(), "no capture was taken, want pending");
        assert!(
            !captures.taken(),
            "before any capture is due, the run has taken them all"
        );

        captures.due(0.5);
        assert!(
            !captures.taken(),
            "after the first of two captures, the run has taken them all"
        );

        captures.due(1.5);
        assert!(
            captures.taken(),
            "after the last capture, the run has not taken them all"
        );
        assert!(
            !captures.pending(),
            "after the last capture, a capture is still pending"
        );
    }

    #[test]
    fn a_directory_with_no_seconds_has_no_captures_and_is_not_finished() {
        let captures = capturing("/frames", Vec::new());
        assert!(!captures.pending(), "a capture is pending, want none");
        assert!(
            !captures.taken(),
            "the captures are taken, want not finished"
        );
    }
}
