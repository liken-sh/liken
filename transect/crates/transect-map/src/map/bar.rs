//! A usage bar: what a pod or a container uses of one resource, against
//! what it requests and is limited to.  The workload page draws one per
//! pod and the pod page one per container, so the two pages show usage the
//! same way.
//!
//! A bar runs to the limit, with the request marked, or, with no limit,
//! to the request.  A limit is the most a container can use, so a bar
//! against it shows how close the container is to being throttled or
//! killed.  When there is no limit, the request is the only size the
//! scheduler has for the container.

use transect_ground::machines::{Amount, BYTES_PER_MI};

/// One resource: what it uses, requests, and is limited to, in cores or
/// bytes.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Bar {
    pub used: Option<f64>,
    /// None when it requests none.
    pub request: Option<f64>,
    pub limit: Option<f64>,
}

impl Bar {
    pub fn new(used: Option<f64>, request: Amount, limit: Option<Amount>) -> Self {
        Self {
            used,
            request: (request != Amount::default()).then(|| request.units()),
            limit: limit.map(Amount::units),
        }
    }

    /// The amount that the bar's full width shows: the limit, or the
    /// request when there is no limit.  With neither there is no bar.
    pub fn scale(&self) -> Option<f64> {
        self.limit.or(self.request)
    }

    pub fn used_percent(&self) -> Option<u32> {
        Some(percent(self.used?, self.scale()?))
    }

    /// Where the request ends: short of the end when the bar runs to a
    /// limit, and the whole bar when it runs to the request.
    pub fn request_percent(&self) -> Option<u32> {
        Some(percent(self.request?, self.scale()?))
    }

    /// Whether the use is more than the bar's scale.  Only a use past a
    /// request with no limit above it can be more.
    pub fn over(&self) -> bool {
        matches!((self.used, self.scale()), (Some(used), Some(scale)) if used > scale)
    }
}

fn percent(part: f64, whole: f64) -> u32 {
    if whole <= 0.0 {
        return 0;
    }
    (part / whole * 100.0).round().clamp(0.0, 100.0) as u32
}

/// Bytes as the bars write them: "355Mi".
pub fn mebibytes(bytes: f64) -> String {
    format!("{:.0}Mi", bytes / BYTES_PER_MI)
}

/// Cores as the bars write them: "200m".
pub fn millicores(cores: f64) -> String {
    format!("{:.0}m", cores * 1000.0)
}

/// A memory bar's tooltip: "memory 355Mi used · 750Mi requested ·
/// 1500Mi limit".
pub fn memory_title(bar: &Bar) -> String {
    title("memory", bar, mebibytes)
}

/// A CPU bar's tooltip: "CPU 2m used · 200m requested · no limit".
pub fn cpu_title(bar: &Bar) -> String {
    title("CPU", bar, millicores)
}

/// What a memory bar with nothing to run to shows in its place:
/// "290Mi · no request · no limit".  None when the bar has a scale.
pub fn memory_unscaled(bar: &Bar) -> Option<String> {
    unscaled(bar, mebibytes)
}

/// "2m · no request · no limit", as `memory_unscaled` for CPU.
pub fn cpu_unscaled(bar: &Bar) -> Option<String> {
    unscaled(bar, millicores)
}

/// The gray line a machine's pod row writes under bars that have no
/// request or limit to run to, in place of figures inside them.  It
/// names the bar that lacks a scale when only one does, and it is none
/// when both have one.
pub fn unscaled_note(memory: &Bar, cpu: &Bar) -> Option<String> {
    let bars = [
        ("mem", memory, mebibytes as fn(f64) -> String),
        ("cpu", cpu, millicores),
    ];
    let unscaled: Vec<_> = bars
        .iter()
        .filter(|(_, bar, _)| bar.scale().is_none())
        .collect();
    let figures: Vec<String> = unscaled
        .iter()
        .filter_map(|(label, bar, amount)| Some(format!("{label} {}", amount(bar.used?))))
        .collect();
    let missing = match unscaled.as_slice() {
        [] => return None,
        [(label, _, _)] => format!("{label} has no request or limit"),
        _ => "no request or limit".to_string(),
    };
    Some(
        figures
            .into_iter()
            .chain([missing])
            .collect::<Vec<_>>()
            .join(" · "),
    )
}

fn unscaled(bar: &Bar, amount: impl Fn(f64) -> String) -> Option<String> {
    if bar.scale().is_some() {
        return None;
    }
    let used = bar.used.map_or("no reading".into(), amount);
    Some(format!("{used} · no request · no limit"))
}

fn title(resource: &str, bar: &Bar, amount: impl Fn(f64) -> String) -> String {
    let used = bar.used.map(|used| format!("{} used", amount(used)));
    let request = bar
        .request
        .map_or("no request".into(), |r| format!("{} requested", amount(r)));
    let limit = bar
        .limit
        .map_or("no limit".into(), |l| format!("{} limit", amount(l)));
    let parts: Vec<String> = used.into_iter().chain([request, limit]).collect();
    format!("{resource} {}", parts.join(" · "))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    #[rstest]
    #[case::both_scaled(bar(None, Some(1.0), None), bar(None, Some(1.0), None), None)]
    #[case::neither_with_readings(
        bar(Some(290.0 * BYTES_PER_MI), None, None),
        bar(Some(0.002), None, None),
        Some("mem 290Mi · cpu 2m · no request or limit")
    )]
    #[case::neither_with_no_reading(
        bar(None, None, None),
        bar(None, None, None),
        Some("no request or limit")
    )]
    #[case::only_cpu(
        bar(None, Some(1.0), None),
        bar(Some(0.002), None, None),
        Some("cpu 2m · cpu has no request or limit")
    )]
    fn notes_the_bars_with_nothing_to_run_to(
        #[case] memory: Bar,
        #[case] cpu: Bar,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(unscaled_note(&memory, &cpu).as_deref(), expected);
    }

    use super::*;

    fn bar(used: Option<f64>, request: Option<f64>, limit: Option<f64>) -> Bar {
        Bar {
            used,
            request,
            limit,
        }
    }

    #[rstest]
    #[case::to_the_limit_with_the_request_marked(
        bar(Some(355.0), Some(750.0), Some(1500.0)),
        Some(24),
        Some(50),
        false
    )]
    #[case::to_the_request_with_no_limit(
        bar(Some(2.0), Some(200.0), None),
        Some(1),
        Some(100),
        false
    )]
    #[case::past_a_request_with_no_limit(
        bar(Some(300.0), Some(200.0), None),
        Some(100),
        Some(100),
        true
    )]
    #[case::to_a_limit_with_no_request(bar(Some(100.0), None, Some(400.0)), Some(25), None, false)]
    #[case::neither(bar(Some(100.0), None, None), None, None, false)]
    #[case::no_reading(bar(None, Some(200.0), Some(400.0)), None, Some(50), false)]
    fn a_bar_runs_to_the_limit_or_the_request(
        #[case] bar: Bar,
        #[case] used: Option<u32>,
        #[case] request: Option<u32>,
        #[case] over: bool,
    ) {
        assert_eq!(
            (bar.used_percent(), bar.request_percent(), bar.over()),
            (used, request, over)
        );
    }

    #[rstest]
    #[case::memory_to_a_limit(
        memory_title(&bar(Some(355.0 * BYTES_PER_MI), Some(750.0 * BYTES_PER_MI), Some(1500.0 * BYTES_PER_MI))),
        "memory 355Mi used · 750Mi requested · 1500Mi limit"
    )]
    #[case::cpu_with_no_limit(
        cpu_title(&bar(Some(0.002), Some(0.2), None)),
        "CPU 2m used · 200m requested · no limit"
    )]
    #[case::nothing(cpu_title(&Bar::default()), "CPU no request · no limit")]
    #[case::unscaled(memory_unscaled(&bar(Some(290.0 * BYTES_PER_MI), None, None)).unwrap(), "290Mi · no request · no limit")]
    #[case::unscaled_unread(cpu_unscaled(&Bar::default()).unwrap(), "no reading · no request · no limit")]
    fn bar_tooltips(#[case] title: String, #[case] expected: &str) {
        assert_eq!(title, expected);
    }
}
