//! Stopping on SIGTERM.  The kubelet sends SIGTERM when it stops the
//! pod, and on a rollout the new pod is already serving.  axum's
//! graceful shutdown stops accepting and waits for every open response
//! to finish, but an event stream never finishes, so after the signal
//! the open streams get a short grace and then the server returns.  The
//! browser's event source reconnects, through the `Service`, to a pod
//! that is still running.

use std::future::Future;
use std::time::Duration;

use axum::Router;
use futures::FutureExt;
use tokio::net::TcpListener;

/// How long open responses, mostly event streams, have to end after the
/// signal.  It is well under the 30 seconds the kubelet waits before
/// SIGKILL.
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
            tracing::info!(?grace, "closing the responses still open after the grace period");
            Ok(())
        }
    }
}

/// Finishes on SIGTERM or on Ctrl-C.
pub async fn signal() {
    let mut terminate =
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(terminate) => terminate,
            Err(error) => {
                tracing::warn!(%error, "cannot listen for SIGTERM; stopping on Ctrl-C only");
                let _ = tokio::signal::ctrl_c().await;
                return;
            }
        };
    tokio::select! {
        _ = terminate.recv() => tracing::info!("SIGTERM: stopping"),
        _ = tokio::signal::ctrl_c() => tracing::info!("interrupted: stopping"),
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

    /// A route whose body never ends, as an event stream's does not.
    fn endless() -> Router {
        Router::new().route(
            "/events",
            get(|| async {
                Body::from_stream(futures::stream::pending::<Result<Vec<u8>, std::io::Error>>())
            }),
        )
    }

    /// Opens a request to `/events` and reads its headers, so the server
    /// holds the response open.
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

        assert!(matches!(served, Ok(Ok(Ok(())))), "{served:?}");
    }
}
