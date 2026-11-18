# Synology CSI

An overlay that adds a `transect.liken.sh/console` annotation to the Synology driver's `CSIDriver`. The annotation is a URL template, and `transect` fills it with the NAS address from the `dsm` parameter of each `StorageClass` and volume of the driver.
With it, the box for the NAS and the page of the driver link to the DSM console of each NAS that the driver provisions on, with no URL written by hand.
