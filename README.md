### Kubernetes operator for service mirroring
---

This operator has been written to prototype the aggregation layer for [Service Mirror](https://linkerd.io/2020/02/25/multicluster-kubernetes-with-service-mirroring/). 

As mentioned in the issue : https://github.com/linkerd/linkerd2/issues/10747, if Services are mirrored from multiple target cluster, currently Service Mirror create cardinal index for each Target cluster ex X-target1, x-target1.

Using this operator, there will be one additional Service created and managed, to aggregate Services from all this target clusters (along side mirrored services).
Ex. x-global it will EndpointSlices from x-target1, x-target2...

#### HOW TO SETUP
---
We use [`just`](https://just.systems/man/en/) to do local setup.

* `just k3d-create` : Command creates 3 k3d local cluster and it also do chaining calls to setup multicluster environment
for linkerd.

* `just run` (`go run main.go` ): Should fireup operator and it will loop for Services labelled using `mirror.linkerd.io/mirrored-service`
