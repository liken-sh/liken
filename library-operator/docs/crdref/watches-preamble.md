A `Watch` is a set of people on one item: three people on a series
together, or one person alone. Progress belongs to the set, so two
`Watch`es on the same series with different people are two records,
and both are right. The people are `Person` names from
`people.liken.sh`, and the item is a movie or a series in a `Library`
of the same namespace.

    apiVersion: library.liken.sh/v1alpha1
    kind: Watch
    metadata:
      name: the-office-together
      namespace: media
    spec:
      people: [chris, thora, io]
      item:
        library: series
        slug: the-office

Write one by hand, or let the browser create one when it asks who is
watching. The operator puts an owner reference on the `Watch` for
each `Person` it names, so the `Watch` goes when the last of them
goes. The status is the operator's: it says which `Play` was recorded
last against this `Watch` and where that `Play` reached, and it is
rewritten as the namespace's progress store records rows. What comes
next in the series is the browser's to work out from its catalog.
