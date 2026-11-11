// The frames of the end of a video, one a second, read from ffmpeg. The
// end titles are in the last minutes of a file, so the decode seeks to the
// start of a window and reads from there to the end. A two-hour film costs
// the decode of its last 15 minutes.
//
// ffmpeg writes raw BGR on its standard output. The `fps` filter gives one
// frame for each second from the seek, so frame n is n seconds after the
// window's start, and the stream needs no times.

use std::io::Read;
use std::path::Path;
use std::process::{Child, ChildStdout, Command, Stdio};

use serde::Deserialize;

use crate::Error;
use crate::picture::Picture;

#[derive(Clone, Debug, PartialEq)]
pub struct Probe {
    pub width: usize,
    pub height: usize,
    pub duration: f64,
}

#[derive(Deserialize)]
struct ProbeOutput {
    streams: Vec<ProbeStream>,
    format: ProbeFormat,
}

#[derive(Deserialize)]
struct ProbeStream {
    width: usize,
    height: usize,
}

#[derive(Deserialize)]
struct ProbeFormat {
    duration: String,
}

pub fn probe(video: &Path) -> Result<Probe, Error> {
    let output = Command::new("ffprobe")
        .args(["-v", "error", "-select_streams", "v:0"])
        .args(["-show_entries", "stream=width,height:format=duration"])
        .args(["-of", "json"])
        .arg(video)
        .output()?;
    if !output.status.success() {
        return Err(String::from_utf8_lossy(&output.stderr).into());
    }
    parse_probe(&output.stdout)
}

fn parse_probe(json: &[u8]) -> Result<Probe, Error> {
    let parsed: ProbeOutput = serde_json::from_slice(json)?;
    let stream = parsed
        .streams
        .first()
        .ok_or("the file has no video stream")?;
    Ok(Probe {
        width: stream.width,
        height: stream.height,
        duration: parsed.format.duration.parse()?,
    })
}

// The width the text detector reads. PP-OCR's detector was evaluated with
// the short side of a picture at 736 pixels. A frame at 736 wide is
// smaller than that, and it found the lines of a credit roll in the
// experiments at 11 frames a second on one CPU.
pub const DETECT_WIDTH: usize = 736;

// The size the frames are decoded to: twice the detector's width, with the
// height in proportion and rounded so that the half-size picture's height
// is a multiple of 32, which the detector's layers need. The rounding
// stretches the picture by 4% at most. The reader crops each line from the
// full frame, where a line of a credit roll is about 30 pixels high.
pub fn decoded_size(probe: &Probe) -> (usize, usize) {
    let half = (DETECT_WIDTH * probe.height) as f64 / probe.width as f64;
    let half = ((half / 32.0).round() as usize).max(1) * 32;
    (DETECT_WIDTH * 2, half * 2)
}

// The second the window starts: a whole second, `window` seconds before
// the end, or the start of a video shorter than the window.
pub fn window_start(duration: f64, window: f64) -> f64 {
    (duration - window).max(0.0).floor()
}

pub struct Frames {
    child: Child,
    stdout: ChildStdout,
    width: usize,
    height: usize,
}

impl Frames {
    pub fn open(
        video: &Path,
        start: f64,
        (width, height): (usize, usize),
        hwaccel: Option<&str>,
        threads: usize,
    ) -> Result<Self, Error> {
        let mut command = Command::new("ffmpeg");
        command.args(["-hide_banner", "-nostdin", "-loglevel", "error"]);
        // ffmpeg starts one decoding thread for each core it sees, and a
        // container sees every core of its node. A fixed count bounds the
        // memory that the frame threads hold.
        command.args(["-threads", &threads.to_string()]);
        let (input, filter) = decoding(hwaccel, width, height);
        command
            .args(input)
            .args(["-ss", &start.to_string()])
            .arg("-i")
            .arg(video)
            .args(["-map", "0:v:0", "-an", "-sn", "-dn"])
            .args(["-vf", &filter])
            .args(["-pix_fmt", "bgr24", "-f", "rawvideo", "-"])
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit());
        let mut child = command.spawn()?;
        let stdout = child.stdout.take().ok_or("ffmpeg has no stdout")?;
        Ok(Frames {
            child,
            stdout,
            width,
            height,
        })
    }

    pub fn finish(mut self) -> Result<(), Error> {
        let status = self.child.wait()?;
        if !status.success() {
            return Err(format!("ffmpeg exited with {status}").into());
        }
        Ok(())
    }
}

impl Iterator for Frames {
    type Item = Result<Picture, Error>;

    fn next(&mut self) -> Option<Self::Item> {
        let mut bgr = vec![0u8; self.width * self.height * 3];
        match self.stdout.read_exact(&mut bgr) {
            Ok(()) => Some(Ok(Picture::new(self.width, self.height, bgr))),
            Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => None,
            Err(e) => Some(Err(e.into())),
        }
    }
}

// The decoder's options and the filter chain that takes one frame a second
// and brings it to width by height. With VA-API the frame stays in GPU
// memory, the GPU scales it and converts it to 8-bit NV12, and only the
// small frame is copied to the CPU. `fps` reads only each frame's time, so
// it runs on frames in GPU memory, before the scale.
//
// A decode on the GPU stops at its first error (-xerror), so `read` can
// decode the window again in software without a wait for the whole file.
fn decoding(hwaccel: Option<&str>, width: usize, height: usize) -> (Vec<String>, String) {
    match hwaccel {
        Some("vaapi") => (
            [
                "-xerror",
                "-hwaccel",
                "vaapi",
                "-hwaccel_output_format",
                "vaapi",
            ]
            .map(String::from)
            .to_vec(),
            format!("fps=1,scale_vaapi=w={width}:h={height}:format=nv12,hwdownload,format=nv12"),
        ),
        Some(other) => (
            vec!["-xerror".into(), "-hwaccel".into(), other.into()],
            format!("fps=1,scale={width}:{height}:flags=bilinear"),
        ),
        None => (
            vec![],
            format!("fps=1,scale={width}:{height}:flags=bilinear"),
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_probe_reads_the_size_and_the_duration() {
        let json = br#"{"streams":[{"width":1920,"height":800}],"format":{"duration":"5400.5"}}"#;
        let expected = Probe {
            width: 1920,
            height: 800,
            duration: 5400.5,
        };
        assert_eq!(parse_probe(json).unwrap(), expected);
    }

    #[test]
    fn parse_probe_refuses_a_file_with_no_video() {
        let json = br#"{"streams":[],"format":{"duration":"10"}}"#;
        assert!(parse_probe(json).is_err());
    }

    fn sized(width: usize, height: usize) -> (usize, usize) {
        decoded_size(&Probe {
            width,
            height,
            duration: 0.0,
        })
    }

    #[test]
    fn decoded_size_rounds_the_half_height_to_32() {
        assert_eq!(sized(1920, 1080), (1472, 832));
        assert_eq!(sized(1920, 800), (1472, 640));
        assert_eq!(sized(720, 480), (1472, 960));
    }

    #[test]
    fn window_start_is_a_whole_second_and_never_negative() {
        assert_eq!(window_start(5400.7, 900.0), 4500.0);
        assert_eq!(window_start(300.0, 900.0), 0.0);
    }

    #[test]
    fn decoding_scales_on_the_gpu_with_vaapi() {
        let (input, filter) = decoding(Some("vaapi"), 1472, 832);
        assert!(input.contains(&"-xerror".to_string()));
        assert!(filter.starts_with("fps=1,scale_vaapi=w=1472:h=832"));
        let (input, filter) = decoding(None, 1472, 832);
        assert!(input.is_empty());
        assert_eq!(filter, "fps=1,scale=1472:832:flags=bilinear");
    }
}
