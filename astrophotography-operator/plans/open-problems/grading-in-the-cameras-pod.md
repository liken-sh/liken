# Grading in the camera's pod

Open problem. Grading reads every frame, so root plan 74 puts it in the
data plane, in the camera's pod, next to the frames. The camera's pod
belongs to `observatory-operator`, and grading belongs to imaging. A
grader container in that pod puts an `astrophotography-operator`
concern inside an `observatory-operator` pod. The two operators need
an agreed way to add a container to a device pod, or grading needs
another home with fast access to the frames. Plate solving has the same
question.
