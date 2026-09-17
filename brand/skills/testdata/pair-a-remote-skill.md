---
name: pair-a-remote
description: "Pair a Bluetooth remote with a machine and map its keys. Use this when a room has a new remote."
---

This skill is the guide at https://media.liken.sh/docs/guides/pair-a-remote/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Pair a remote

A remote pairs with the machine that holds the adapter. Read
[the adapter guide](https://media.liken.sh/docs/guides/adapter/) first, and the
[Remote reference](https://media.liken.sh/docs/reference/remote/#spec) for every field.

Put the remote in pairing mode, then run:

```sh
kubectl get remotes
# [the reference](/docs/reference/remote/) is not a link here
```

Check [the keys](#keys) once the remote answers, and read
[the model's own notes](https://example.com/remotes/) for the codes.
Mail [the list](mailto:remotes@example.com) when a model is missing.

![the mark](https://media.liken.sh/brand/liken.svg "A patch of crustose lichen.")

## Keys

The key map is on the Remote. See [the key map](https://media.liken.sh/docs/guides/pair-a-remote/keys/) for the names,
and run:

    kubectl describe remote living-room
