# k3s

A `LoadBalancerController` for the k3s service load balancer. For a `LoadBalancer` `Service`, k3s runs pods that open the `Service`'s ports on each machine as host ports.
With it, the "Ways in" page at `/access` lists those pods' machines on the row of their `Service`, instead of listing each pod's host ports as a way in of its own.
