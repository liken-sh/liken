# Roll a lab release onto `liken-1`

Contents:

* Where the testbed's `Cluster` document is
* Check that the port is free
* Build the release
* Serve the channel
* Fetch the release through the laptop's address
* Point the testbed's `Cluster` at the release
* Wait for every machine
* Return the testbed to the public channel

The physical testbed, `liken-1` and `stick-1`, can install a lab
release from the laptop's `make serve`, the same way the dev cluster
does. Use it when a drill needs real hardware: a device, a firmware,
or a module that a QEMU guest does not have.

The testbed's `Cluster` document is not in this repository. It is in
the maintainer's own repository, next to a `kubectl` wrapper for the
testbed. That repository's instructions say how to apply and record a
change to it. Ask the maintainer for its path when the session does
not have it.

Run the `make` commands from `liken/`. Each step says what it must
show before you start the next one.

1. **Check that the port is free.** `make serve` listens on `:8017`.
   A `liken serve` that an earlier drill left running can already use
   the port:

   ```bash
   ss -ltnp | grep ':8017' || echo free
   ```

   Go on when it prints `free`. Otherwise stop the old server by its
   PID.

2. **Build the release.** The build takes tens of minutes, so put a
   timeout on it and write its output to a file:

   ```bash
   timeout 3000 make release VERSION=$(date +%Y.%m.%d)-901 \
       > <scratch>/release.log 2>&1; echo rc=$?
   grep -A2 'catalog entry' <scratch>/release.log
   ```

   Go on when it prints `rc=0` and the catalog entry, with its
   `version` and `digest`. When `releases/dist/` already has a `-901`
   for the day, use the next free `-9xx` serial.

3. **Serve the channel.** Start the server under `setsid`, so that it
   keeps running when the harness cleans up the task that started it:

   ```bash
   setsid make serve </dev/null ><scratch>/serve.log 2>&1 &
   ```

   `liken serve` listens on `:8017` on every interface. The testbed
   is not on QEMU's user-mode network. So it connects to the laptop
   at the laptop's address on the network they share, not at
   10.0.2.2.

4. **Fetch the release through that address.** From the laptop, use
   the address that the testbed will use:

   ```bash
   curl -fsS http://<laptop address>:8017/releases/channel.yaml | head -5
   curl -fsS http://<laptop address>:8017/releases/<version>/release.yaml | sha256sum
   ```

   Go on when the channel responds and the SHA-256 equals the digest
   of the catalog entry from step 2. A different SHA-256 means that
   the server serves a different `releases/dist`.

5. **Point the testbed's `Cluster` at the release.** In the testbed's
   `Cluster` document, make these changes:

   * Set `spec.releases.source` to
     `http://<laptop address>:8017/releases`.
   * Add the catalog entry from step 2 to `spec.releases.catalog`.
     Keep the entry of the release that the testbed runs now.
   * Set `spec.version` to the new version.

   Apply the document with the testbed's `kubectl` wrapper. Then read
   the two fields back:

   ```bash
   ./kubectl apply -f cluster.yaml
   ./kubectl get cluster -o jsonpath='{.items[0].spec.version} {.items[0].spec.releases.source}{"\n"}'
   ```

   Go on when both fields show the new values. The rules of step 3 of
   the upgrade drill in `SKILL.md` apply here too. The field is
   `spec.version`, and both version fields use the format
   `yyyy.mm.dd-nnn`.

6. **Wait for every machine.** Each machine downloads the release,
   reboots in its turn, and proves the new slot. Wait with a deadline,
   and print the machines only when a line changes:

   ```bash
   V=<version>
   deadline=$(( $(date +%s) + 2400 )); last=""
   while (( $(date +%s) < deadline )); do
       s=$(./kubectl get machines --no-headers 2>/dev/null)
       [[ -n "$s" && "$s" != "$last" ]] && { date -u +%T; echo "$s"; last="$s"; }
       ./kubectl get machines -o json 2>/dev/null | jq -e --arg v "$V" \
           '[.items[] | select(.status.version.liken != $v or .status.phase != "Ready")] | length == 0' \
           >/dev/null && break
       sleep 20
   done
   ```

   The drill starts when every machine is `Ready` on the new version.
   Keep `make serve` running until then, because a machine that has
   not moved yet still downloads from it.

7. **Return the testbed to the public channel.** When the drill ends,
   set `spec.releases.source` back to `https://releases.liken.sh`. Set
   `spec.version` to a published release that has an entry in the
   catalog. Apply the document, and wait for the machines as in
   step 6. Then stop `liken serve` by its PID, and check that `:8017`
   is free, as the "Clean up" section of `SKILL.md` says.
