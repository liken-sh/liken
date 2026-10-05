# Where the field of view comes from

Open problem. The plate solver's index files depend on the field of
view, and the field of view depends on the focal length and on the
camera's sensor size and pixel size. The camera reports its sensor in
`CCD_INFO` only after it connects. The resource could state the sensor,
or the reconciler in `observatory-operator` could read it from the
camera and write it to status. The choice decides whether the index
files can be fetched before the camera is ever connected. Root plan 74
covers the index cache under "Plate solving".
