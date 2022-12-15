# A Multi-cluster SD-WAN east-west gateway

The goal of this project is to build an east-west (EW) gateway to seamlessly interconnect two or
more service-mesh clusters over an SD-WAN fabric, integrating the L4/L7 traffic management policies
on the service-mesh side with the L3/L4 policies on the SD-WAN interconnect, as well as the
observability and the security functions, for a consistent end-to-end user experience.

## Table of contents

1. [Overview](#overview)
1. [User stories](#user-stories)
1. [Service-level traffic management](#service-level-traffic-management)
1. [L7 traffic management](#l7-traffic-management)
1. [Resiliency](#resiliency)
1. [Observability](#observability)
1. [Getting started](#getting-started)
1. [License](#license)

## Overview

The idea is to provide consistent end-to-end traffic management, security and observability
features across a multi-cluster service mesh deployment interconnected by an SD-WAN fabric.

![Multi-cluster service-mesh/SD-WAN integration architecture](/doc/multi-cluster-service-mesh-reference-arch.svg)

The plan is to realize this goal as follows:

* Services between clusters are exported/imported using the [Multi-cluster Services API
  (KEP-1645)](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
  CRDs, extended with a set of annotations to control SD-WAN routing policies. 
* The import/export policies are rendered into regular [Kubernetes Gateway
  API](https://gateway-api.sigs.k8s.io) CRDs and implemented on top of a standard [Kubernetes
  gateway implementation](https://gateway-api.sigs.k8s.io/implementations).
* The EW gateways exchange inter-cluster traffic in a way so that the SD-WAN fabric interconnecting
  the clusters can classify the traffic to/from different services into distinct traffic classes
  and deliver the required service level to each traffic class as specified by the operator.
* L7 policies, with SD-WAN-related semantics, can be added by the user to control the way traffic
  egresses from, and ingresses into, a cluster.

We default to pure unencrypted HTTP throughout for simplicity. It is trivial to enforce encryption
by rewriting all rules to HTTPS.

## User stories

We envision the following use cases.

* **Service-level SD-WAN policies.** The `payment.secure` HTTP service (port 8080), deployed into
cluster-1, serves sensitive data, so the service owner in cluster-1 wants to secure access to this
service, by forcing all queries/responses to this service to be sent over the SD-WAN interconnect
in the fastest and most secure way. At the same time, the `logging.insecure` service serves
less-sensitive bulk traffic, so the corresponding traffic exchange defaults to the Internet.

* **SD-WAN aware L7 traffic management.** Same scenario as above, but now only GET queries to the
`http://payment.secure:8080/payment` API endpoint are considered sensitive, access to
`http://payment.secure:8080/stats` is irrelevant (defaults to the Internet), and any other access
to the service is denied.

* **Resiliency:** One of the clusters goes down. The EW gateways automatically initiate a
circuit-breaking for the failed EW-gateway endpoint and retry all failed connections to another
cluster where healthy backends are still running.

* **Monitoring:** Operators want end-to-end monitoring across the clusters. EW-gateways add a
`spanid` header to all HTTP(S) traffic exchanged over the SD-WAN interconnect to trace requests
across the service-mesh clusters and the SD-WAN.

## Service-level traffic management

In the simplest model, the service owner can associate SD-WAN policies at the level of each
distinct Kubernetes service, but there is no way to impose additional L7-level SD-WAN policies on
top. This basic model will then be extended to a fully-fledged L7 model later.

### WAN policies

Each global service can have a WAN policy associated with it (see below). The SD-WAN policies are
defined by a CRD named `WANPolicy.mcw.l7mp.io`.  The format of this CRD is completely unspecified
for now; below is a sample that is enough for the purposes of this note. At this point the best
option seems to be if we make these CRDs cluster-global, to avoid that WAN policies installed into
different namespaces somehow end up conflicting.

```yaml
apiVersion: mcw.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
name: sd-wan-priority-high
spec:
  tunnel: business
  port: 31111
  sla:
    jitter-ms: 50
    latency-ms: 100
    loss-percent: 1
---
apiVersion: mcw.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-low
spec:
  tunnel: default
  port: 31112
  sla:
    jitter-ms: 1000
    latency-ms: 1000
    loss-percent: 100
```

Note that the same WANPolicy CRs must exist on all clusters attached to the clusterset and they
must specify the same SLAs/ports.

### Service export

In order to allow access from other clusters, a service has to be explicitly exported from the
hosting cluster. 

Suppose that in the exporting cluster the payment service is defined as follows. Note that the
resultant FQDN for the service will be `payment.secure.svc.cluster.local`.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: payment
  namespace: secure
spec:
  selector:
    app: payment
  ports:
  - port: 8080
    protocol: TCP
```

One can use the `ServiceExport.multicluster.k8s.io` CRD from the similarly named CRD from the
[Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
for controlling exported services. The only difference is that our ServiceExports can contain
additional annotations to encode the SD-WAN policy assigned by the service owner to the exported
service.

One possible way to do that is to explicitly specify the SD-WAN tunnel we want the service to be
assigned to. Below is sample ServiceExport for exporting the `payment.secure` service from
cluster-1 over the SD-WAN tunnel `sd-wan-priority-high`.

```yaml
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment
  namespace: secure
  annotations:
    mcw.l7mp.io/mc-wan-policy: sd-wan-priority-high
```

The name/namespace of the ServiceExport must the same as that of the service to be exported. The
SD-WAN policy is specified using the `mcw.l7mp.io/mc-wan-policy: sd-wan-priority-high` annotation;
this selects the tunnel associated with the `sd-wan-priority-high` SD-WAN policy for exchanging all
traffic of the `payment.secure` service across the SD-WAN. The SD-WAN policies will be specified by
a separate CRD below. Note that we cannot enforce these priorities on the receiver side (by the
time we receive the request on the ingress EW gateway it has already passed via the SD-WAN), so the
SD-WAN priorities will be enforced on the sender side.

### Service import

An exported service will be imported only by clusters in which the service's namespace
exists. Service imports are represented with a CRD `ServiceImport.multicluster.k8s.io`, which is
identical to the similarly named CRD from the [Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api). The
CRD is automatically created in the importing cluster and the control plane is supposed to be fully
responsible for the lifecycle of a ServiceImport.

A sample ServiceImport corresponding to the above ServiceExport is shown below.

```yaml
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceImport
metadata:
  name: payment
  namespace: secure
spec:
  type: ClusterSetIP
  ports:
  - name: http
    protocol: TCP
    port: 8080
```


At this point, the exported `payment.secure` service can be reached from the importing cluster by
issuing a HTTP request to the host `payment.secure.svc.clusterset.local`.

## L7 traffic management

Up to this point, SD-WAN policies could be applied to individual Kubernetes services only, but
there is no way to distinguish SD-WAN policies based on the API endpoint or certain HTTP header
values. The below shows how to add L7 traffic management policies to the basic specification by
reusing the above ServiceImport/ServiceExport CRs.

### Server-side L7 policies

Suppose the service owner wants to filter incoming requests on the HTTP header: to the path
`/payment` only `GET` requests are allowed, everything else is denied. Assuming the server-side
cluster runs Istio, the below VirtualService should do the trick (the below assumes the default
`spec.gateways: mesh` so the L7 policy will be enforced at all pods running the `payment.secure`
service).

``` yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payment
  namespace: secure
spec:
  hosts:
  - payment.secure.svc.clusterset.local
  http:
  - match:
    - uri:
        prefix: "/payment"
      method:
        exact: GET
    route:
    - destination:
        host: payment.secure.svc.cluster.local
```

### Client-side L7 policies

Suppose the client consuming the `payment.secure` in the importing cluster wishes to apply
additional L7 policies: e.g., for supporting canary deployment for the `payment.secure`
service. Suppose the `prod` cluster exports the service `payment-v1.secure` that runs the stable
version of the backend software (this will create a ServiceImport with the FQDN
`payment-v1.secure.svc.clusterset.local` on the client side), while the `dev` cluster exports the
experimental service `payment-v2.secure` (with the ServiceImport FQDN
`payment-v2.secure.svc.clusterset.local` on the client side). In this case the following will
VirtualService on the importing side will send all requests containing the cookie `user=test` to
the v2 service, and everything else to the v1 service (again assuming Istio).

``` yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payment
  namespace: secure
spec:
  hosts:
  - payment.secure.svc.clusterset.local
  http:
  - match:
      headers:
        cookie:
          regex: "^(.*?;)?(user=test)(;.*)?"
    route:
    - destination:
        host: payment-v2.secure.svc.clusterset.local
  - route:
    - destination:
        host: payment-v1.secure.svc.clusterset.local
```

Note that the VirtualService uses the ServiceImport FQDNs as `destination` services, so once we
have the SD-WAN compatible ServiceImport/ServiceExport logics we do not have to add additional L7
support, just rely on Istio to provide us the required L7 capabilities.

Another possibility is to route requests from the clients over different SD-WAN tunnels depending
on the HTTP headers. For instance, only GET requests to the `/payment` path would be routed to the
high-priority tunnel, everything else should go the low-prio tunnel. 

First, the server-side cluster must export two services, one over each SD-WAN tunnel, as follows:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: payment-high-prio
  namespace: secure
spec:
  selector:
    app: payment
  ports:
  - port: 8080
    protocol: TCP
---
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment-high-prio
  namespace: secure
  annotations:
    mcw.l7mp.io/mc-wan-policy: sd-wan-priority-high
---
apiVersion: v1
kind: Service
metadata:
  name: payment-low-prio
  namespace: secure
spec:
  selector:
    app: payment
  ports:
  - port: 8080
    protocol: TCP
---
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment-low-prio
  namespace: secure
  annotations:
    mcw.l7mp.io/mc-wan-policy: sd-wan-priority-low
```

Then, these will create two ServiceImports on the client-side, one with the FQDN
`payment-high-prio.secure.svc.clusterset.local` that will be routed via the high-priority tunnel
and another one with the FQDN `payment-low-prio.secure.svc.clusterset.local` that will be routed to
the low-prio tunnel. These ServiceImports can again be used as destinations for the
VirtualServices.

``` yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payment
  namespace: secure
spec:
  hosts:
  - payment.secure.svc.clusterset.local
  http:
  - match:
    - uri:
        prefix: "/payment"
      method:
        exact: GET
    route:
    - destination:
        host: payment-high-prio.secure.svc.clusterset.local
  - route:
    - destination:
        host: payment-low-prio.secure.svc.clusterset.local
```

## Resiliency

Implementing health-check/retry/timeout/circuit-breaking policies can supported via the L7
capabilities provided by Istio's L7 traffic management policies.

## Observability

Adding a `spanid` HTTP header to each connection crossing the SD-WAN is already within the
capabilities of the framework, but maybe at a certain point we could provide some automation around
this.

## Getting started

(*Work in progress*)

A proof-of-concept tool is available that automates the ServiceImport and ServiceExport
workflows. For now, the tool provides a simple imperative interface, whereby the user explicitly
performs service imports/exports, providing all the necessary parameters on the command
line. Later, this tool will be developed into a Kubernetes operator to automatically reconcile the
ServiceImport/ServiceExport CRDs.

### Prerequisites

It is assumed two Kubernetes clusters are available, Istio with the built-in [Gateway API
implementation](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api) is
installed into both clusters, and `kubectl` is configured with the necessary credentials to reach
both clusters, with the context for each of the clusters available in the environment variables
`$CTX1` and `$CTX2`.

### Installation

Clone the repository and build the `mcwanctl` command line tool.

``` console
cd mc-wan
go build -o mcwanctl main.go
```

### Service export

Export the `payment.secure` service from cluster-1 over the high-priority SD-WAN tunnel.

``` console
mcwanctl --context $CTX1 export payment/secure --wan-policy=high
```

This call will set up the server-side pipeline to ingest requests to the `payment.secure` service
into the cluster and route them to the proper backend pods. 

The below command will query the status of a service-export, providing the SD-WAN policy associated
with the service exposition and a `GW_IP_ADDRESS` that can be used to reach the service from other
clusters.

``` console
mcwanctl --context $CTX1 status payment/secure
```

The below command deletes a service export.

``` console
mcwanctl --context $CTX1 unexport payment/secure
```

### Service import

In and of itself a service export will not do much; to actually reach an exported service a cluster
needs to explicitly import it. The below will import the `payment.secure` service into cluster-2.

``` console
mcwanctl --context $CTX2 import payment/secure --ingress-gw=<GW_IP_ADDRESS>
```

At this point, requests from cluster-2 to `http://payment.secure.svc.clusterset.local:8000` should
be routed through the high-priority SD-WAN tunnel and land at one of the backend pods in cluster-1.

Finally, remove a service import by unimporting it.

``` console
mcwanctl --context $CTX2 unimport payment/secure
```

## License

Copyright 2021-2022 by its authors. Some rights reserved. See [AUTHORS](/AUTHORS).

MIT License - see [LICENSE](/LICENSE) for full text.
