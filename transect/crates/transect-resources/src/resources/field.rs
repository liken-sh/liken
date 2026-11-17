//! One domain's part of the loaded resources, read from the watch of
//! every resource.  The binary holds the type with a field for each
//! domain, and each domain names only its own field's type, so a domain
//! low in the crate order reads its resources without naming the
//! binary's type.

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use tokio::sync::watch;

/// The value of one field of the resources, as of the latest load, and
/// each change to the resources after it.
pub struct Field<T> {
    source: Box<dyn Source<T>>,
}

impl<T: 'static> Field<T> {
    /// What `read` reads of each load `receiver` sends.
    pub fn new<L: Send + Sync + 'static>(
        receiver: watch::Receiver<Arc<L>>,
        read: fn(&L) -> T,
    ) -> Self {
        Self {
            source: Box::new(Projection {
                receiver,
                field: read,
            }),
        }
    }

    /// The field now.
    pub fn read(&self) -> T {
        self.source.read()
    }

    /// The field now, marking the load seen, so `changed` waits for the
    /// next one.
    pub fn read_and_update(&mut self) -> T {
        self.source.read_and_update()
    }

    /// Marks the load now seen, so `changed` waits for the next one.
    pub fn mark_unchanged(&mut self) {
        self.source.mark_unchanged();
    }

    /// Waits for the next load, or fails when nothing will load again.
    pub async fn changed(&mut self) -> Result<(), watch::error::RecvError> {
        self.source.changed().await
    }
}

impl<T: Clone + Send + Sync + 'static> Field<T> {
    /// `value`, which never changes, for a test or for `transect` run
    /// with no resources directory.
    pub fn fixed(value: T) -> Self {
        Self::new(watch::channel(Arc::new(value)).1, T::clone)
    }
}

impl<T: 'static> Clone for Field<T> {
    fn clone(&self) -> Self {
        Self {
            source: self.source.boxed(),
        }
    }
}

impl<T> std::fmt::Debug for Field<T> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Field")
    }
}

type Changed<'a> = Pin<Box<dyn Future<Output = Result<(), watch::error::RecvError>> + Send + 'a>>;

trait Source<T>: Send + Sync {
    fn read(&self) -> T;
    fn read_and_update(&mut self) -> T;
    fn mark_unchanged(&mut self);
    fn changed(&mut self) -> Changed<'_>;
    fn boxed(&self) -> Box<dyn Source<T>>;
}

struct Projection<L, T> {
    receiver: watch::Receiver<Arc<L>>,
    field: fn(&L) -> T,
}

impl<L: Send + Sync + 'static, T: 'static> Source<T> for Projection<L, T> {
    fn read(&self) -> T {
        (self.field)(&self.receiver.borrow())
    }

    fn read_and_update(&mut self) -> T {
        (self.field)(&self.receiver.borrow_and_update())
    }

    fn mark_unchanged(&mut self) {
        self.receiver.mark_unchanged();
    }

    fn changed(&mut self) -> Changed<'_> {
        Box::pin(self.receiver.changed())
    }

    fn boxed(&self) -> Box<dyn Source<T>> {
        Box::new(Self {
            receiver: self.receiver.clone(),
            field: self.field,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Default)]
    struct Loaded {
        number: u32,
    }

    #[tokio::test]
    async fn reads_the_field_of_each_load() {
        let (sender, receiver) = watch::channel(Arc::new(Loaded { number: 1 }));
        let mut field = Field::new(receiver, |loaded: &Loaded| loaded.number);
        assert_eq!(field.read_and_update(), 1);
        sender.send_replace(Arc::new(Loaded { number: 2 }));
        field.changed().await.unwrap();
        assert_eq!(field.read(), 2);
    }
}
