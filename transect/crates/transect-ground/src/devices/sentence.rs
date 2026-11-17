//! The fixed words of the text that the page shows for held devices.
//! The rest of that text is names from a claim or a slice.

use super::Off;
use transect_graph::edges::Device;

/// The most devices a short list names before it counts the rest.
pub const NAMED: usize = 2;

impl Off {
    /// Why no machine strip names a device.
    pub fn note(self) -> &'static str {
        match self {
            Self::NotServed => "holds is off: the cluster does not serve resource.k8s.io/v1",
            Self::Forbidden => {
                "holds is off: transect may not list ResourceClaims or ResourceSlices"
            }
            Self::Retrying => "holds is off for now: retrying discovery of resource.k8s.io/v1",
        }
    }
}

/// The first `NAMED` devices by their short names, then a count of the
/// rest: "gpu card-0, radio usb-1 +3".
pub fn few(devices: &[Device]) -> String {
    let named: Vec<String> = devices.iter().take(NAMED).map(Device::short).collect();
    let named = named.join(", ");
    match devices.len().saturating_sub(NAMED) {
        0 => named,
        rest => format!("{named} +{rest}"),
    }
}

/// Every device by its full name, one per line, for a title.
pub fn all(devices: &[Device]) -> String {
    let full: Vec<String> = devices.iter().map(Device::full).collect();
    full.join("\n")
}

/// The devices a pod holds, for its placement line.  The line names no
/// machine when every device is on the machine the pod runs on, and
/// names each device's own machine otherwise.
pub fn pod_holds(devices: &[Device], runs_on: Option<&str>) -> Option<String> {
    let on_its_machine = devices
        .iter()
        .all(|device| device.machine.is_some() && device.machine.as_deref() == runs_on);
    let names: Vec<String> = devices
        .iter()
        .map(|device| match (&device.machine, on_its_machine) {
            (Some(machine), false) => format!("{} on {machine}", device.short()),
            _ => device.short(),
        })
        .collect();
    let list = match names.as_slice() {
        [] => return None,
        [one] => one.clone(),
        [rest @ .., last] => format!("{} and {last}", rest.join(", ")),
    };
    Some(format!("holds {list}"))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::fixtures::device;

    #[rstest]
    #[case::a_domain(device("gpu.example.com", "alpha", "card-0"), "gpu card-0")]
    #[case::a_bare_name(device("radio", "alpha", "usb-1"), "radio usb-1")]
    fn names_a_device_by_its_drivers_first_label(#[case] device: Device, #[case] short: &str) {
        assert_eq!(device.short(), short);
    }

    #[rstest]
    #[case::one(&["card-0"], "gpu card-0")]
    #[case::two(&["card-0", "card-1"], "gpu card-0, gpu card-1")]
    #[case::more(&["card-0", "card-1", "card-2", "card-3"], "gpu card-0, gpu card-1 +2")]
    fn names_a_few_and_counts_the_rest(#[case] names: &[&str], #[case] text: &str) {
        let devices: Vec<Device> = names
            .iter()
            .map(|name| device("gpu.example.com", "alpha", name))
            .collect();
        assert_eq!(few(&devices), text);
    }

    #[rstest]
    #[case::none(vec![], Some("alpha"), None)]
    #[case::one_on_its_machine(
        vec![device("gpu.example.com", "alpha", "card-0")],
        Some("alpha"),
        Some("holds gpu card-0")
    )]
    #[case::two_on_its_machine(
        vec![device("gpu.example.com", "alpha", "card-0"), device("radio.example.com", "alpha", "usb-1")],
        Some("alpha"),
        Some("holds gpu card-0 and radio usb-1")
    )]
    #[case::one_elsewhere(
        vec![device("gpu.example.com", "beta", "card-0")],
        Some("alpha"),
        Some("holds gpu card-0 on beta")
    )]
    #[case::not_scheduled(
        vec![device("gpu.example.com", "beta", "card-0")],
        None,
        Some("holds gpu card-0 on beta")
    )]
    fn says_what_a_pod_holds(
        #[case] devices: Vec<Device>,
        #[case] runs_on: Option<&str>,
        #[case] text: Option<&str>,
    ) {
        assert_eq!(pod_holds(&devices, runs_on).as_deref(), text);
    }
}
