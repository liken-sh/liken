// The binary reads the flags and the wiring. A bad flag stops the run before a
// window opens.

use idle_screen::client::{CLIENT, Client};
use idle_screen::harness::options::help;
use idle_screen::harness::{self, Invocation, Options};
use idle_screen::metrics;
use idle_screen::wiring::Wiring;

fn main() {
    // The environment is read once here. The client takes the broker, the
    // topics, and the seeds, and the harness takes the window grace, which
    // arms the watchdog.
    let wiring = Wiring::from_environment();

    // The listener starts before the client opens its own reader, so a
    // scrape between start and the first bus session still reads a stated
    // value. A failure here never stops the run: the screen this pod exists
    // to draw does not depend on a Prometheus reading it.
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
