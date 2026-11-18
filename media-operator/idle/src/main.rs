// The idle screen binary. It reads the environment and the flags, and
// exits on an invalid flag before it opens a window.

use idle_screen::client::{CLIENT, Client};
use idle_screen::harness::options::help;
use idle_screen::harness::{self, Invocation, Options};
use idle_screen::metrics;
use idle_screen::wiring::Wiring;

fn main() {
    let wiring = Wiring::from_environment();

    // In a pod, `supervise` runs this binary again as a child process and
    // starts the child again after each compositor restart
    // (`harness/respawn.rs`). The parent exits here, before the metrics
    // listener starts, so only the child binds the metrics port.
    if let Some(status) = harness::respawn::supervise(wiring.window_grace.is_some()) {
        std::process::exit(status);
    }

    // Start the listener before `Client::open` connects to the bus, so a
    // scrape before the first bus session returns the metrics.
    if let Some(address) = metrics::address(&wiring.screen.metrics_address) {
        metrics::serve(address, CLIENT, &wiring.screen.version);
    }

    match Options::parse(std::env::args().skip(1)) {
        Ok(Invocation::Help) => print!("{}", help()),
        Ok(Invocation::Run(mut options)) => {
            options.window_grace = wiring.window_grace;
            if let Err(error) = harness::run(Client::open(wiring), options) {
                eprintln!("idle-screen: {error}");
                std::process::exit(1);
            }
        }
        Err(error) => {
            eprintln!("idle-screen: {error}");
            std::process::exit(2);
        }
    }
}
