# Where the field of view comes from

Open problem. The plate solver's index files depend on the field of
view, and the field of view depends on the focal length and on the
camera's sensor size and pixel size. `observatory-operator` writes the
sensor from `CCD_INFO` to the `Camera`'s `status.info`, but only after
the camera has connected for the first time. The open choice is
whether a resource also states the sensor, so that the index files can
be fetched before the camera ever connects, or whether the first night
with a new camera waits for its index files.
