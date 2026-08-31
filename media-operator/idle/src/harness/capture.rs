// The frames a run writes to disk: which frame is due, where it goes, and the
// PNG the renderer's readback becomes.
//
// The schedule is a function of the clock alone, so a test drives it with
// numbers and never opens a window.

use std::path::{Path, PathBuf};

/// The directory the frames go in, the seconds they were asked for, and a
/// cursor into that list. The cursor only moves forward, so a capture fires
/// once.
#[derive(Debug)]
pub struct Captures {
    dir: PathBuf,
    at: Vec<f64>,
    next: usize,
}

impl Captures {
    /// The captures `--capture` and `--capture-at` asked for, or nothing. The
    /// directory is where a frame goes, so a run that named none captures
    /// nothing whatever seconds it listed.
    pub fn requested(dir: Option<PathBuf>, at: Vec<f64>) -> Option<Self> {
        Some(Self {
            dir: dir?,
            at,
            next: 0,
        })
    }

    /// The path this frame is written to, if the frame is the first one at or
    /// after the next capture second.
    pub fn due(&mut self, at: f64) -> Option<PathBuf> {
        let when = *self.at.get(self.next)?;
        if at < when {
            return None;
        }
        self.next += 1;
        Some(self.dir.join(format!("{when:06.2}.png")))
    }

    /// Whether a capture is still to come. The harness draws every frame it
    /// can while one is, so the run reaches the second it was asked for.
    pub fn pending(&self) -> bool {
        self.next < self.at.len()
    }

    /// Whether the run has taken every capture it asked for. A run that named
    /// a directory and no second takes none and ends its own way.
    pub fn taken(&self) -> bool {
        !self.at.is_empty() && !self.pending()
    }
}

/// Write one captured frame. `rgba` is what the renderer read back.
pub fn write_png(path: &Path, width: u32, height: u32, rgba: &[u8]) {
    if let Some(dir) = path.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    let Some(buffer) = image::RgbaImage::from_raw(width, height, rgba.to_vec()) else {
        eprintln!(
            "capture {}: {} bytes is not {width}x{height}",
            path.display(),
            rgba.len()
        );
        return;
    };
    if let Err(error) = buffer.save(path) {
        eprintln!("capture {}: {error}", path.display());
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
        assert!(Captures::requested(None, vec![0.5]).is_none());
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
    fn a_run_has_taken_its_captures_after_the_last_one() {
        let mut captures = capturing("/frames", vec![0.5, 1.5]);
        assert!(captures.pending());
        assert!(!captures.taken());

        captures.due(0.5);
        assert!(!captures.taken());

        captures.due(1.5);
        assert!(captures.taken());
        assert!(!captures.pending());
    }

    #[test]
    fn a_directory_with_no_seconds_captures_nothing_and_ends_nothing() {
        let captures = capturing("/frames", Vec::new());
        assert!(!captures.pending());
        assert!(!captures.taken());
    }
}
