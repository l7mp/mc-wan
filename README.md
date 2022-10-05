# A Multi-cluster service mesh east-west gateway for SD-WAN

The goal of this project is to build an east-west (EW) gateway to seamlessly interconnect two or
more service-mesh clusters over an SD-WAN fabric, integrating the L4/L7 traffic management policies
on the service-mesh side with the L3/L4 policies on the SD-WAN interconnect, as well as the
observability and the security functions, into a comprehensive end-to-end user experience.

## Table of contents

1. [Overview](#overview)
1. [User stories](#user-stories)
1. [Concepts](#concepts)
1. [Description](#description)
1. [License](#license)

## Overview

The plan is to realize this goal as follows:

* Services between clusters are exported/imported using the [Multi-cluster Services API
  (KEP-1645)](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
  CRDs, extended with L7 policies and SD-WAN-related semantics to control the way traffic egresses
  from, and ingresses into, a cluster.
* The import/export policies are rendered into regular [Kubernetes Gateway
  API](https://gateway-api.sigs.k8s.io) CRDs and implemented on top of a standard [Kubernetes
  gateway implementation](https://gateway-api.sigs.k8s.io/implementations).
* The EW gateways exchange inter-cluster traffic in a way so that the SD-WAN fabric interconnecting
  the clusters can classify the traffic to/from different services into distinct traffic classes
  and deliver the required service level to each traffic class as specified by the operator.

We default to pure unencrypted HTTP throughout for simplicity. It is trivial to enforce encryption
by rewriting all rules to HTTPS.

## User stories

We envision the following use cases.

* **Receiver-controlled default SD-WAN policies.** The `payment.secure` HTTP service (port 8080),
deployed into cluster-1, serves sensitive data, so the service owner in cluster-1 wants to secure
access to this service, by forcing all queries/responses to this service to be sent over the SD-WAN
interconnect in the fastest and most secure way. At the same time, the `logging.insecure` service
serves less-sensitive bulk traffic, so the corresponding traffic exchange defaults to the Internet.

* **Ingress L7 traffic management on the receiver side.** Same scenario as above, but now only GET
queries to the `http://payment.secure:8080/payment` API endpoint are considered sensitive, access
to `http://payment.secure:8080/stats` is irrelevant (defaults to the Internet), and any other
access to the service is denied.

* **Egress L7 traffic management rules on the sender side.** Same scenario as above, but now all
queries sent between clusters must be tagged with an HTTP header field called `sender-cluster` at
the egress EW gateway in order to indicate the sending cluster's identity to the service backends
on the receiver side. In addition, the sender can also override the service-owner's default SD-WAN
policies if they want.

* **Resiliency:** Regular Internet connection to one of the clusters goes down. The EW gateways
automatically initiate a circuit-breaking for the failed EW-gateway endpoint and retry all failed
connections over either the SD-WAN or another cluster where healthy backends are still running.

* **Monitoring:** Task operators want end-to-end monitoring across the clusters. EW-gateways add a
`spanid` header to all HTTP(S) traffic exchanged over the SD-WAN interconnect to trace requests
across the service-mesh clusters and the SD-WAN.

## Concepts

Below we describe the high-level EW Gateway API we expose to the cluster operators.

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

We introduce a CRD called `ServiceExport.mc-wan.l7mp.io` for controlling exported services. Our
ServiceExport CRD is essentially the same as the identically named CRD from the [Multi-cluster
Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api),
with the exception that our CRD (1) encodes the SD-WAN policy of the service owner and (2)
specifies the receiver-side L7 traffic management policies.

Below is sample ServiceExport for exporting the `payment.secure` service from cluster-1:

```yaml
apiVersion: mc-wan.l7mp.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment
  namespace: secure
spec:
  http:
    rules:
      - matches:
          - method: GET
            path:
              type: PathPrefix
              value: /payment
        backendRefs:
          group: mc-wan.l7mp.io
          kind: WANPolicy
          name: sd-wan-priority-high
      - matches:
          - path:
              type: PathPrefix
              value: /stats
        backendRefs:
          group: mc-wan.l7mp.io
          kind: WANPolicy
          name: sd-wan-priority-low
```

Note that the name/namespace of the ServiceExport is the same as that of the service to be exported
and `spec.http.rules` is a list of standard
[`HTTPRouteRule`](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRouteRule)
objects from the Kubernetes Gateway API. Each rule can specify a `backendRef` to the CRDs named
`sd-wan-priority-high` and `sd-wan-priority-low`, which represent the SD-WAN policy to be applied
to HTTP traffic matching the rule (see below). Note that we cannot enforce these priorities on the
receiver side (by the time we receive the request on the ingress EW gateway it has already passed
via the SD-WAN), so these priorities serve only as a default priority to the sender side (unless
they decide to override the default priority).

### Service import

In order to access an exported service, the sender-side cluster must explicitly import the target
service. Note that before the ServiceImport is created there is no service on the sender side: it
is the job of the service-import-controller to make sure that the adequate "shadow" services
backing the ServiceImports are created.

We introduce a `ServiceImport.mc-wan.l7mp.io` CRD for controlling service imports.  Our
ServiceImport CRD is essentially the same as the identically named CRD from the [Multi-cluster
Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api),
with the exception that our CRD (1) encodes the preferred SD-WAN policy and (2) specifies the
sender-side L7 traffic management policies.

Below is sample ServiceImport for importing the `payment.secure` service into cluster-2:

```yaml
apiVersion: mc-wan.l7mp.io/v1alpha1
kind: ServiceImport
metadata:
  name: payment
  namespace: secure
spec:
  ports:
  - name: http
    protocol: TCP
    port: 9080
  http:
    rules:
      - filter:
          requestHeaderModifier:
            add:
              name: origin-cluster
              value: cluster-2
        backendRefs:
          group: mc-wan.l7mp.io
          kind: WANPolicy
          name: sd-wan-priority-high
```

Note that the name/namespace of the ServiceImport is the same as that of the service to be
imported. In addition, `spec.ports` is a standard `ServicePort` object from the [Multi-cluster
Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
SerrviceImport CRDs, and `spec.http.rules` is a list of standard
[`HTTPRouteRule`](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRouteRule)
objects from the Kubernetes Gateway API. Each rule may optionally specify a `backendRef` to the one
of the WANPolicy CRDs in order to override the SD-WAN priority set by the service owner on the
receiver side (here both import and the export side specify the same WAN policy to there is no
override).

### WAN policies

ServiceExport and ServiceImport resources may associate a WAN policy with each HTTP filter, which
is represented by a CRD named `WANPolicy.mc-wan.l7mp.io`.  The format of this CRD is completely
unspecified for now; below is a sample that is enough for the purposes of this note. At this point
the best option seems to be if we make these CRDs cluster-global, to avoid that WAN policies
installed into different namespaces somehow end up conflicting.

```yaml
apiVersion: mc-wan.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-high
spec:
  tunnel: business
  port: 31111
---
apiVersion: mc-wan.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-low
spec:
  tunnel: default
  port: 31112
---
apiVersion: mc-wan.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-internet
spec:
  tunnel: ""
  port: 31113
```

In a full design we may write the requested SLAs here or anything else the SD-WAN will be able to
enforce.

## Description

We deconstruct our ServiceImports and ServiceExports to actual Kubernetes resources/objects and use
existing implementations to encode the policies in our EW gateways. In contrast, WANPolicy
resources do not appear explicitly in any of the gateway pipelines; these resources are purely
virtual and serve only (1) for the user to be able to indicate the chosen WAN policy in service
imports and exports and (2) for the control plane to drive the selection of the destination port in
the egress gateway pipeline (see below).

### Interconnect fabric: SD-WAN

For each SD-WAN priority level (say, `business` and `default`), we enforce a separate HTTP(s) EW-EW
HTTP(S) session, otherwise if everything goes over a single HTPP(S) stream then the SD-WAN may not
be able to classify the inter-cluster traffic to apply the forwarding preferences.

We apply the following rules.
* All SD-WAN priority levels (i.e., tunnels) are wrapped by a CRD that specifies the port used for
  sending traffic over the SD-WAN with that priority (say, 31111 for high-prio traffic).
* All access to the receiver side EW-gateway that is to receive high priority on the SD-WAN uses
  some predefined port X that we specify in the corresponding WANPolicy resource.
* We may define a separate port for traffic that is to be exchanged over the Internet.

We assume the SD-WAN is bootstrapped with appropriate per-destination-port application-aware
routing policies to enforce the priority encoded in the destination port.

### EW gateways

We use a standard [Kubernetes gateway
implementation](https://gateway-api.sigs.k8s.io/implementations) that supports a sufficiently broad
subset of the Gateway API. Maybe the best choice would be
[Istio](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api): that way we
could use further service-mesh functionality *inside the clusters* as well.

We assume that the EW gateway pods are labeled with `app.kubernetes.io/name: gateway` or whatever
the implementation we choose uses for this purpose. 

#### Ingress gateway pipeline

The ingress gateway pipeline contains the necessary Kubernetes resources for the receiving side
cluster to ingest inter-cluster traffic from the SD-WAN for services exported from the cluster.

##### Logistics

We bootstrap the EW gateway with an HTTP listener for each SD-WAN port (X, X+1,...). This will
serve for ingesting the traffic from the SD-WAN into the cluster.

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: Gateway
metadata:
  name: mc-wan-ingress-gateway
  namespace: mc-wan
spec:
  gatewayClassName: whatever
  listeners:
  - name: mc-wan-high-prio-sd-wan-listener
    protocol: HTTP
    port: 31111
    allowedRoutes:
      namespaces:
        from: Same
  - name: mc-wan-low-prio-sd-wan-listener
    protocol: HTTP
    port: 31112
    allowedRoutes:
      namespaces:
        from: Same
  - name: mc-wan-internet-listener
    protocol: HTTP
    port: 31113
    allowedRoutes:
      namespaces:
        from: Same
```

We assume all these services are exposed using an appropriate Kubernetes service. We recommend
using the NodePort for the first two listeners that we want to route through the SD-WAN (we can
enforce this by adding a route to the sending clusters that routes the node-ip-range of the cluster
to the vEdge), and exposing the `mc-wan-internet-listener` with a LoadBalancer service to route it
via the default Internet.

##### Compiling the ServiceExport

This ServiceExport is compiled into the below standard Kubernetes
[HTTPRoute](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRoute)
resource, which we attach to the above Gateway.

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: HTTPRoute
metadata:
  name: payment-secure
  namespace: mc-wan
spec:
  parentRefs:
  - name: mc-wan-ingress-gateway
  hostnames:
    - payment.secure.svc.cluster.local
  rules:
    - matches:
        - method: GET
          path:
            type: PathPrefix
            value: /payment
      filter:
        urlRewrite:
          hostname: payment.secure.svc.cluster.local
      backendRefs:
        - name: payment.secure.svc.cluster.local
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /stats
      filter:
        urlRewrite:
          hostname: payment.secure.svc.cluster.local
      backendRefs:
        - name: payment
          namespace: secure
          port: 8080
```

The `http.rules` object is copied verbatim from the ServiceExport to the HTTPRoute, except that we
rewrite the `hostname` to the original name of the target service and set the `backendRefs` to
refer to the exported service `payment.secure` over port 8080.

We may need to add an [annotation](https://gateway-api.sigs.k8s.io/guides/multiple-ns) or a
`PolicyTargetReference` as well to allow cross-namespace routing.
    
#### Egress gateway pipeline

The egress gateway pipeline contains the necessary Kubernetes resources for the sender side cluster
to send traffic over the SD-WAN to the receiver side for the services that are imported into the
cluster. Note that only those clusters would receive requests from that actually (1) export the
corresponding service and (2) have healthy backend pods for the service.

##### Logistics

We bootstrap the egress pipeline with a set of dummy services that will allow routing the requests
from the sender-side cluster to the proper receiver-side EW gateway(s) that have actual
backends/pods serving the requested service. In particular, for any cluster participating in the
multi-cluster fleet, we create a service whose endpoints contain the externally reachable IP
address of the corresponding EW gateway.

In the running example, we represent the EW gateway of cluster-1 on the sender side with the a
dummy service and the corresponding Endpoint object.

1. We create a dummy service that will represent the IP address(es) of the EW gateways on the
   remote clusters (cluster-1 for now).  Note that the service deliberately has [no
   selectors](https://kubernetes.io/docs/concepts/services-networking/service/#services-without-selectors):
   in such cases Kubernetes does not create the Endpoint object to back the service, so we can
   create one manually and add the IP address of the EW gateways of the target clusters explicitly.
   
   ```yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: mc-wan-cluster-1-target
     namespace: mc-wan
   spec:
     ports:
       - protocol: TCP
         port: 31111
       - protocol: TCP
         port: 31112
       - protocol: TCP
         port: 31113
   ```
   
   Note that we list all SD-WAN ports: we want to reuse the same service across all egress EW
   gateway policies.

1. We manually create the Endpoint object for the dummy service and list the IP address of the
   Gateway that we want to receive the corresponding traffic (the IP addresses of the EW gateway in
   cluster-1 in our case, i.e., `IP_1`).

   ```yaml
   apiVersion: v1
   kind: Endpoints
   metadata:
     name: mc-wan-cluster-1-target-endpoint
     namespace: mc-wan
   subsets:
     - addresses:
         - ip: IP_1
   ```

The idea here is that any query sent to the `mc-wan-cluster-1-target` service will be forwarded to
the EW gateway of cluster-1, and we assume that all participating clusters are wrapped with one
such service/endpoint pair on every other cluster. We will use these dummy services below as
backends for the egress gateway policies.

#### Compiling the ServiceImport

Compiling the ServiceImport to Kubernetes APIs is a tad bit more complex than on the export
side. The below resources are created for the above sample service import. 

1. We create a shadow service in the `mc-wan` namespace. The idea is that whenever an application
   wants to send a query to the global `payment.secure` service they must use the shadow service
   instead of the original service, with the port specified in the ServiceImport (if no port is
   specified in the ServiceImport then we fall back to the original port on the receiver side).
   
   ```yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: payment-secure-clusterset
     namespace: mc-wan
   spec:
     selector:
       app.kubernetes.io/name: gateway
     ports:
       - name: HTTP
         protocol: TCP
         port: 9080
   ```
   
   Note that we set the selector to the gateway pods: that way, any request to the service will
   land at one of our EW gateway pods.

1. We open a Gateway listener at the target port to receive the query on our EW gateway.

   ```yaml
   apiVersion: gateway.networking.k8s.io/v1beta1
   kind: Gateway
   metadata:
     name: payment-secure
     namespace: mc-wan
   spec:
     gatewayClassName: whatever
     listeners:
     - name: payment-secure-egress-gateway
       protocol: HTTP
       port: 9080
       allowedRoutes:
         namespaces:
           from: Same
   ```

   An actual implementation will not create a separate Gateway per service-import but it will
   rather just add another HTTP listener to a global `egress-http-gateway` instance, but for the
   purpose of this spec it is easier to explain what's going on this way.

1. We add a HTTPRoute that represents the L7 rules to be applied on the sender side and to send the
   query over to the other side. For that purpose, we will use a `backendRef` to the dummy service
   created in the bootstrapping step to direct the matching packets to the receiver-side EW
   gateway.

   ```yaml
   apiVersion: gateway.networking.k8s.io/v1beta1
   kind: HTTPRoute
   metadata:
     name: payment-secure
     namespace: mc-wan
   spec:
     parentRefs:
     - name: payment-secure
     hostnames:
       - payment-secure.mcw.svc.cluster.local
       - payment.secure.svc.cluster.local
     rules:
       - filter:
           requestHeaderModifier:
             add:
               name: origin-cluster
               value: cluster-2
         backendRefs:
           - name: mc-wan-cluster-1-target
             namespace: mc-wan
             port: 31111
             weight: 1
   ```

   Note that we listen to both the original `payment.secure` hostname as well as to that of the
   shadow service `payment-secure.mcw` for safety. Note further that the `backendRef` of the rule
   points to the dummy service wrapping the ingress EW gateway that will receive the packets
   matching the filter, the target port is the one that belongs to the selected SD-WAN priority,
   and finally the weight is set to 1.  Here, the ServiceExport specifies the
   `sd-wan-priority-high` WAN policy for the `payment.secure` service and the ServiceImport does
   not override this, so we select the corresponding port from the matching WANPolicy,
   i.e., 31111. In addition, the weight allows us to load-balance requests across the receiver side
   clusters based on the number of pods allocated in each cluster; e.g., if cluster-X has 3 pods
   for the `payment.secure` service and cluster-Y has 4 pods, then the corresponding weights will
   be as follows.


   ```yaml
   ...
         backendRefs:
           - name: mc-wan-cluster-X-target
             namespace: mc-wan
             port: 31111
             weight: 3
           - name: mc-wan-cluster-Y-target
             namespace: mc-wan
             port: 31111
             weight: 4
   ```

### Resiliency

Implementing health-check/retry/timeout/circuit-breaking policies is subject to the appearance of
the corresponding features in the Gateway API. Some
[thinking](https://github.com/kubernetes-sigs/gateway-api/issues/97) is already going on in that
direction: once these APIs become available it is straightforward to add them to our ServiceImport
and ServiceExport CRDs.

### Observability

Adding a `spanid` HTTP header to each connection crossing the SD-WAN is already within the
capabilities of the framework, but maybe at a certain point we could provide some automation around
this.

## License

Copyright 2021-2022 by its authors. Some rights reserved. See [AUTHORS](/AUTHORS).

MIT License - see [LICENSE](/LICENSE) for full text.
