// The keyframes of a video, read from ffmpeg. `-skip_frame nokey` makes the
// decoder decode only the keyframes, and encoders place a keyframe at most
// cuts, so the sample is close to one frame per shot at a fraction of a full
// decode. ffmpeg writes raw BGR on its standard output, which carries no
// time, so the `showinfo` filter prints each frame's time on standard error
// in the same order, and the reader pairs the two streams frame by frame.

use std::io::{BufRead, BufReader, Read};
use std::path::Path;
use std::process::{Child, ChildStdout, Command, Stdio};
use std::sync::mpsc::{Receiver, channel};
use std::thread;

use serde::Deserialize;

use crate::picture::Picture;
use crate::runtime::Error;

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

// The size the frames are decoded to: the video's own size, or narrower
// when the video is wider than `max_width`, with the height kept in
// proportion and even. A 4K frame decodes to 1280 wide, because SFace sees
// a 112 by 112 crop of every face and the extra pixels only cost memory.
pub fn decoded_size(probe: &Probe, max_width: usize) -> (usize, usize) {
    if probe.width <= max_width {
        return (probe.width, probe.height);
    }
    let height = (probe.height * max_width / probe.width) / 2 * 2;
    (max_width, height)
}

pub struct Keyframe {
    pub time: f64,
    pub picture: Picture,
}

pub struct Keyframes {
    child: Child,
    stdout: ChildStdout,
    times: Receiver<f64>,
    width: usize,
    height: usize,
}

impl Keyframes {
    pub fn open(
        video: &Path,
        width: usize,
        height: usize,
        hwaccel: Option<&str>,
        threads: usize,
    ) -> Result<Self, Error> {
        let mut command = Command::new("ffmpeg");
        command.args(["-hide_banner", "-nostdin", "-loglevel", "info"]);
        // ffmpeg starts one decoding thread for each core it sees, and a
        // container sees every core of its node. Each frame thread holds
        // its own frames, so a 4K HEVC decode on a 22-thread machine held
        // 1.7 GB. A fixed count bounds the memory.
        command.args(["-threads", &threads.to_string()]);
        let (input, filter) = decoding(hwaccel, width, height);
        command
            .args(input)
            .args(["-skip_frame", "nokey", "-i"])
            .arg(video)
            .args(["-map", "0:v:0", "-an", "-sn", "-dn"])
            .args(["-vf", &filter])
            // Without passthrough, ffmpeg duplicates frames to fill a
            // constant rate, and each copy would count as another sample.
            .args(["-fps_mode", "passthrough"])
            .args(["-pix_fmt", "bgr24", "-f", "rawvideo", "-"])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let mut child = command.spawn()?;
        let stdout = child.stdout.take().ok_or("ffmpeg has no stdout")?;
        let stderr = child.stderr.take().ok_or("ffmpeg has no stderr")?;
        let (send, times) = channel();
        thread::spawn(move || {
            for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                if let Some(time) = showinfo_time(&line)
                    && send.send(time).is_err()
                {
                    break;
                }
            }
        });
        Ok(Keyframes {
            child,
            stdout,
            times,
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

impl Iterator for Keyframes {
    type Item = Result<Keyframe, Error>;

    fn next(&mut self) -> Option<Self::Item> {
        let mut bgr = vec![0u8; self.width * self.height * 3];
        match self.stdout.read_exact(&mut bgr) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => return None,
            Err(e) => return Some(Err(e.into())),
        }
        let time = match self.times.recv() {
            Ok(time) => time,
            Err(_) => return Some(Err("ffmpeg wrote a frame with no showinfo time".into())),
        };
        Some(Ok(Keyframe {
            time,
            picture: Picture::new(self.width, self.height, bgr),
        }))
    }
}

// The decoder's options and the filter chain that brings a decoded frame to
// width by height. With VA-API, the frame stays in GPU memory, the GPU scales
// it and converts it to 8-bit NV12, and only the small frame is copied to the
// CPU. A 4K frame is 25 MB as BGR, and copying each one before a CPU scale
// cost more time than the GPU's decode saved. showinfo runs after the copy,
// because it reads frames in CPU memory.
fn decoding(hwaccel: Option<&str>, width: usize, height: usize) -> (Vec<String>, String) {
    match hwaccel {
        Some("vaapi") => (
            ["-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"]
                .map(String::from)
                .to_vec(),
            format!("scale_vaapi=w={width}:h={height}:format=nv12,hwdownload,format=nv12,showinfo"),
        ),
        Some(other) => (
            vec!["-hwaccel".into(), other.into()],
            format!("showinfo,scale={width}:{height}:flags=bilinear"),
        ),
        None => (
            vec![],
            format!("showinfo,scale={width}:{height}:flags=bilinear"),
        ),
    }
}

// showinfo prints one line for each frame with its number and its
// presentation time, such as `[Parsed_showinfo_0 @ 0x55] n:   3 pts:
// 13013 pts_time:13.5135 duration: ...`. Its other lines, for side data
// and color, carry no `pts_time:`.
fn showinfo_time(line: &str) -> Option<f64> {
    if !line.contains("Parsed_showinfo") {
        return None;
    }
    let rest = line.split("pts_time:").nth(1)?;
    rest.split_whitespace().next()?.parse().ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn showinfo_time_reads_the_frame_line() {
        let line =
            "[Parsed_showinfo_0 @ 0x5581] n:   3 pts:  13013 pts_time:13.5135 duration:   1001";
        assert_eq!(showinfo_time(line), Some(13.5135));
    }

    #[test]
    fn showinfo_time_skips_the_side_data_lines() {
        let line = "[Parsed_showinfo_0 @ 0x5581]   color_range:tv color_space:bt709";
        assert_eq!(showinfo_time(line), None);
    }

    #[test]
    fn showinfo_time_skips_other_filters() {
        assert_eq!(showinfo_time("[out#0/rawvideo @ 0x1] pts_time:4"), None);
    }

    #[test]
    fn vaapi_scales_on_the_gpu_before_the_copy() {
        let (input, filter) = decoding(Some("vaapi"), 1280, 720);
        assert_eq!(
            (input.join(" "), filter),
            (
                "-hwaccel vaapi -hwaccel_output_format vaapi".to_string(),
                "scale_vaapi=w=1280:h=720:format=nv12,hwdownload,format=nv12,showinfo".to_string()
            )
        );
    }

    #[test]
    fn software_decoding_scales_on_the_cpu() {
        let (input, filter) = decoding(None, 1280, 720);
        assert_eq!(
            (input, filter),
            (vec![], "showinfo,scale=1280:720:flags=bilinear".to_string())
        );
    }

    #[test]
    fn parse_probe_reads_the_size_and_length() {
        let json =
            br#"{"streams":[{"width":3840,"height":2160}],"format":{"duration":"6608.208000"}}"#;
        let probe = parse_probe(json).unwrap();
        assert_eq!(
            probe,
            Probe {
                width: 3840,
                height: 2160,
                duration: 6608.208
            }
        );
    }

    #[test]
    fn decoded_size_keeps_a_frame_that_fits() {
        let probe = Probe {
            width: 1920,
            height: 800,
            duration: 1.0,
        };
        assert_eq!(decoded_size(&probe, 1920), (1920, 800));
    }

    #[test]
    fn decoded_size_narrows_a_4k_frame() {
        let probe = Probe {
            width: 3840,
            height: 2160,
            duration: 1.0,
        };
        assert_eq!(decoded_size(&probe, 1920), (1920, 1080));
    }
}
