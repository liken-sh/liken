//! Graceful shutdown on SIGTERM. axum's graceful shutdown stops
//! accepting connections and waits for every open response to end, but
//! an event stream never ends. So after the signal the server waits
//! `GRACE` and then returns anyway. The browser's event source then
//! reconnects through the `Service` to another pod, which during a
//! rollout is already serving.

use std::future::Future;
use std::time::Duration;

use axum::Router;
use futures::FutureExt;
use tokio::net::TcpListener;

/// How long open responses have to end after the signal. It is well
/// under the 30 seconds that the kubelet waits by default before it
/// sends SIGKILL. `deploy/transect.yaml` does not change that default.
pub const GRACE: Duration = Duration::from_secs(5);

/// Serves `router` on `listener` until `stop` finishes, then waits at
/// most `grace` for open responses before returning.
pub async fn serve_until(
    listener: TcpListener,
    router: Router,
    stop: impl Future<Output = ()> + Send + 'static,
    grace: Duration,
) -> std::io::Result<()> {
    let stop = stop.boxed().shared();
    let server = axum::serve(listener, router).with_graceful_shutdown(stop.clone());
    let deadline = async {
        stop.await;
        tokio::time::sleep(grace).await;
    };
    tokio::select! {
        served = server.into_future() => served,
        () = deadline => {
            tracing::info!(?grace, "Closing the open responses because the grace period ended");
            Ok(())
        }
    }
}

/// Finishes on SIGTERM or on Ctrl-C.
pub async fn signal() {
    let mut terminate = match tokio::signal::unix::signal(
        tokio::signal::unix::SignalKind::terminate(),
    ) {
        Ok(terminate) => terminate,
        Err(error) => {
            tracing::warn!(%error, "Cannot listen for SIGTERM, so only Ctrl-C stops the server");
            let _ = tokio::signal::ctrl_c().await;
            return;
        }
    };
    tokio::select! {
        _ = terminate.recv() => tracing::info!("Received SIGTERM, stopping"),
        _ = tokio::signal::ctrl_c() => tracing::info!("Received Ctrl-C, stopping"),
    }
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use axum::Router;
    use axum::body::Body;
    use axum::routing::get;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::{TcpListener, TcpStream};
    use tokio::sync::oneshot;

    /// A route whose body never ends, like an event stream's body.
    fn endless() -> Router {
        Router::new().route(
            "/events",
            get(|| async {
                Body::from_stream(futures::stream::pending::<Result<Vec<u8>, std::io::Error>>())
            }),
        )
    }

    /// Requests `/events` and reads the status line, so the server has
    /// an open response when the test stops it.
    async fn open_stream(address: std::net::SocketAddr) -> TcpStream {
        let mut stream = TcpStream::connect(address).await.unwrap();
        stream
            .write_all(b"GET /events HTTP/1.1\r\nhost: transect\r\n\r\n")
            .await
            .unwrap();
        let mut head = [0; 12];
        stream.read_exact(&mut head).await.unwrap();
        assert_eq!(&head, b"HTTP/1.1 200");
        stream
    }

    #[tokio::test]
    async fn stops_within_the_grace_though_a_stream_is_open() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (stop, stopped) = oneshot::channel::<()>();
        let grace = Duration::from_millis(100);
        let server = tokio::spawn(super::serve_until(
            listener,
            endless(),
            async {
                let _ = stopped.await;
            },
            grace,
        ));
        let _stream = open_stream(address).await;

        stop.send(()).unwrap();
        let served = tokio::time::timeout(Duration::from_secs(5), server).await;

        assert!(
            matches!(served, Ok(Ok(Ok(())))),
            "the server finished with {served:?}, want a clean stop within 5 seconds"
        );
    }
}
