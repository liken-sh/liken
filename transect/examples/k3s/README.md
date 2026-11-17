# k3s

A `LoadBalancerController` for k3s's service load balancer, whose pods answer a `LoadBalancer` `Service`'s ports on each machine with host ports.
With it, Ways in counts those pods' machines on their `Service`'s row and does not list each pod's host ports as a way in of its own.
