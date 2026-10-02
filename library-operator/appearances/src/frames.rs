// The samples of a video, read from ffmpeg: every keyframe, and a frame
// every second between them. A keyframe stands for up to 10 seconds, and
// inside that time people turn, enter, and leave, so a sample every second
// tracks who is on screen. A frame between keyframes needs the frames it
// references, so ffmpeg decodes every frame and the `select` filter passes on
// only the samples. On the bench of nine films the full decode took 109 to
// 250 seconds for a 1080p film and 368 seconds for a 4K film on a laptop
// iGPU, against 8 to 26 seconds for the keyframes alone. A sample every 2
// seconds costs nearly the same, because the decode is the fixed cost.
//
// ffmpeg writes raw BGR on its standard output, which carries no time, so
// the `showinfo` filter prints each frame's time and its keyframe flag on
// standard error in the same order, and the reader pairs the two streams
// frame by frame.

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

// The longest time between two samples, in seconds.
pub const SAMPLE_EVERY: f64 = 1.0;

pub struct Sample {
    pub time: f64,
    pub keyframe: bool,
    pub picture: Picture,
}

// What showinfo prints for one frame.
#[derive(Clone, Copy, Debug, PartialEq)]
struct FrameInfo {
    time: f64,
    keyframe: bool,
}

pub struct Samples {
    child: Child,
    stdout: ChildStdout,
    frames: Receiver<FrameInfo>,
    width: usize,
    height: usize,
}

impl Samples {
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
        let scale_on_gpu = hwaccel == Some("vaapi") && gpu_scales(video, width, height, threads);
        if hwaccel == Some("vaapi") && !scale_on_gpu {
            eprintln!(
                "{}: the GPU cannot scale these frames, so the CPU scales them",
                video.display()
            );
        }
        let (input, filter) = decoding(hwaccel, scale_on_gpu, width, height);
        command
            .args(input)
            .arg("-i")
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
        let (send, frames) = channel();
        thread::spawn(move || {
            for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                if let Some(frame) = showinfo(&line)
                    && send.send(frame).is_err()
                {
                    break;
                }
            }
        });
        Ok(Samples {
            child,
            stdout,
            frames,
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

impl Iterator for Samples {
    type Item = Result<Sample, Error>;

    fn next(&mut self) -> Option<Self::Item> {
        let mut bgr = vec![0u8; self.width * self.height * 3];
        match self.stdout.read_exact(&mut bgr) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => return None,
            Err(e) => return Some(Err(e.into())),
        }
        let frame = match self.frames.recv() {
            Ok(frame) => frame,
            Err(_) => return Some(Err("ffmpeg wrote a frame with no showinfo time".into())),
        };
        Some(Ok(Sample {
            time: frame.time,
            keyframe: frame.keyframe,
            picture: Picture::new(self.width, self.height, bgr),
        }))
    }
}

// The filter that passes on the samples: the first frame, every keyframe,
// and each frame at least SAMPLE_EVERY after the sample before it. The count
// starts again at each keyframe, so the first sample of a shot is its
// keyframe and the next is a second after it. `select` reads only each
// frame's time and flags, so it runs on frames in GPU memory, before the
// scale.
fn select() -> String {
    format!("select='isnan(prev_selected_t)+key+gte(t-prev_selected_t,{SAMPLE_EVERY})'")
}

// Whether the GPU's video processor scales this video's frames. Some GPUs
// decode a format that their video processor cannot convert: an older
// Intel GPU decodes 10-bit HEVC, but scale_vaapi fails on its frames before
// the first one, with "the requested VAProfile is not supported". The test
// decodes the video's first 2 seconds through the same chain, and costs
// about a second. On a 10-bit film, a GPU decode with the scale on the CPU
// ran at 4.8 times real time, and a software decode at 2.5.
fn gpu_scales(video: &Path, width: usize, height: usize, threads: usize) -> bool {
    Command::new("ffmpeg")
        .args(["-v", "error", "-nostdin", "-threads", &threads.to_string()])
        .args(["-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"])
        .args(["-t", "2", "-i"])
        .arg(video)
        .args(["-map", "0:v:0", "-an", "-sn", "-dn"])
        .arg("-vf")
        .arg(format!(
            "scale_vaapi=w={width}:h={height}:format=nv12,hwdownload,format=nv12"
        ))
        .args(["-f", "null", "-"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|status| status.success())
}

// The decoder's options and the filter chain that picks the samples and
// brings each to width by height. With VA-API, the frame stays in GPU memory, the GPU scales
// it and converts it to 8-bit NV12, and only the small frame is copied to the
// CPU. A 4K frame is 25 MB as BGR, and copying each one before a CPU scale
// cost more time than the GPU's decode saved. showinfo runs after the copy,
// because it reads frames in CPU memory. Where the GPU cannot scale the
// frames, as gpu_scales tells, the GPU decodes and ffmpeg copies each frame
// to the CPU, which selects and scales as a software decode does.
fn decoding(
    hwaccel: Option<&str>,
    scale_on_gpu: bool,
    width: usize,
    height: usize,
) -> (Vec<String>, String) {
    match hwaccel {
        Some("vaapi") if scale_on_gpu => (
            ["-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"]
                .map(String::from)
                .to_vec(),
            format!(
                "{},scale_vaapi=w={width}:h={height}:format=nv12,hwdownload,format=nv12,showinfo",
                select()
            ),
        ),
        Some(other) => (
            vec!["-hwaccel".into(), other.into()],
            format!(
                "{},showinfo,scale={width}:{height}:flags=bilinear",
                select()
            ),
        ),
        None => (
            vec![],
            format!(
                "{},showinfo,scale={width}:{height}:flags=bilinear",
                select()
            ),
        ),
    }
}

// showinfo prints one line for each frame with its number, its
// presentation time, and its keyframe flag, such as `[Parsed_showinfo_1 @
// 0x55] n:   3 pts:  13013 pts_time:13.5135 duration: ... iskey:1 type:I`.
// Its other lines, for side data and color, carry no `pts_time:`.
fn showinfo(line: &str) -> Option<FrameInfo> {
    if !line.contains("Parsed_showinfo") {
        return None;
    }
    let value = |key: &str| line.split(key).nth(1)?.split_whitespace().next();
    Some(FrameInfo {
        time: value("pts_time:")?.parse().ok()?,
        keyframe: value("iskey:") == Some("1"),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn showinfo_reads_the_time_and_the_keyframe_flag() {
        let cases = [
            ("iskey:1 type:I", true),
            ("iskey:0 type:B", false),
            ("type:P", false),
        ];
        for (flags, keyframe) in cases {
            let line = format!(
                "[Parsed_showinfo_1 @ 0x5581] n:   3 pts:  13013 pts_time:13.5135 duration:   1001 {flags} checksum:D386"
            );
            assert_eq!(
                showinfo(&line),
                Some(FrameInfo {
                    time: 13.5135,
                    keyframe
                }),
                "{flags}"
            );
        }
    }

    #[test]
    fn showinfo_skips_the_side_data_lines() {
        let line = "[Parsed_showinfo_1 @ 0x5581]   color_range:tv color_space:bt709";
        assert_eq!(showinfo(line), None);
    }

    #[test]
    fn showinfo_skips_other_filters() {
        assert_eq!(showinfo("[out#0/rawvideo @ 0x1] pts_time:4"), None);
    }

    const SELECT: &str = "select='isnan(prev_selected_t)+key+gte(t-prev_selected_t,1)'";

    #[test]
    fn vaapi_selects_and_scales_on_the_gpu_before_the_copy() {
        let (input, filter) = decoding(Some("vaapi"), true, 1280, 720);
        assert_eq!(
            (input.join(" "), filter),
            (
                "-hwaccel vaapi -hwaccel_output_format vaapi".to_string(),
                format!(
                    "{SELECT},scale_vaapi=w=1280:h=720:format=nv12,hwdownload,format=nv12,showinfo"
                )
            )
        );
    }

    // A GPU whose video processor cannot scale the source's frames, such
    // as 10-bit HEVC on an older Intel GPU, still decodes them. ffmpeg
    // copies each decoded frame to the CPU, and the CPU selects and scales.
    #[test]
    fn vaapi_without_gpu_scaling_decodes_on_the_gpu_and_scales_on_the_cpu() {
        let (input, filter) = decoding(Some("vaapi"), false, 1280, 720);
        assert_eq!(
            (input.join(" "), filter),
            (
                "-hwaccel vaapi".to_string(),
                format!("{SELECT},showinfo,scale=1280:720:flags=bilinear")
            )
        );
    }

    #[test]
    fn software_decoding_selects_and_scales_on_the_cpu() {
        let (input, filter) = decoding(None, false, 1280, 720);
        assert_eq!(
            (input, filter),
            (
                vec![],
                format!("{SELECT},showinfo,scale=1280:720:flags=bilinear")
            )
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
