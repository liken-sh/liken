# The app-id is a guessable string

Open problem. The routing table maps each output to one fixed app-id,
the published device's name. Any client that can reach the compositor's
socket can take a screen by setting that app-id, because the string is
the whole credential and the string is public in the ResourceSlice.

The socket's exposure bounds the risk today. The compositor's directory
is mounted only into pods whose claim was prepared for this driver, so
a client has to be scheduled onto the node with a display claim before
it can guess anything. A minted app-id per allocation would turn the
string into a capability and close the gap.

What blocks minting is the compositor. Weston reads a section's
`app-ids=` once, at the moment it creates the output, so a fresh app-id
per allocation has no path into a running compositor. Minting therefore
costs a restart per allocation, which ends every session on the node.
That trade is recorded in
[plan 02](../completed/02-an-output-for-every-connector.md) with the rest of the
runtime routing design.

No answer is chosen.
