# A Multi-cluster service mesh east-west gateway for SD-WAN

The goal of this project is to build an east-west (EW) gateway to seamlessly interconnect two or
more service-mesh clusters over an SD-WAN fabric, integrating the L4/L7 traffic management policies
on the service-mesh side with the L3/L4 policies on the SD-WAN interconnect, as well as the
observability and the security functions, for a consistent end-to-end user experience.

## Table of contents

1. [Overview](#overview)
1. [User stories](#user-stories)
1. [Service-level traffic management](#service-level-traffic-management)
   1. [Concepts](#concepts)
   1. [Mechanics](#mechanics)
<!-- 1. [L7 traffic management](#l7-traffic-management) -->
<!--    1. [Concepts](#concepts) -->
<!--    1. [Mechanics](#mechanics) -->
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

### Concepts

Below we describe the high-level EW Gateway API we expose to the cluster operators.

#### Service export

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

We use the `ServiceExport.multicluster.k8s.io` CRD from the [Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
for controlling exported services. The only difference is that our ServiceExports can contain
additional annotations to encode the SD-WAN policy assigned by the service owner to the exported
service.

Below is sample ServiceExport for exporting the `payment.secure` service from cluster-1:

```yaml
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: payment
  namespace: secure
  annotations:
    mcw.l7mp.io/mc-wan-policy: sd-wan-priority-high
```

Note that the name/namespace of the ServiceExport is the same as that of the service to be
exported. Note further that the service owner can add the L7 policies on top of this service export
by installing a set of [Gateway and
HTTPRouteRule](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRouteRule)
resources in the exporting cluster. The SD-WAN policy is specified using the
`mcw.l7mp.io/mc-wan-policy: sd-wan-priority-high` annotation: this selects the tunnel associated
with the `sd-wan-priority-high` SD-WAN policy for exchanging all traffic of the `payment.secure`
service across the SD-WAN. The SD-WAN policies will be specified by a separate CRD below. Note that
we cannot enforce these priorities on the receiver side (by the time we receive the request on the
ingress EW gateway it has already passed via the SD-WAN), so the SD-WAN priorities will be enforced
on the sender side.

#### Service import

An exported service will be imported only by clusters in which the service's namespace
exists. Service imports are represented with a CRD `ServiceImport.multicluster.k8s.io` from the
[Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api). The
CRD is automatically created in the importing cluster and the control plane is supposed to be fully
responsible for the lifecycle of a ServiceImport.

#### WAN policies

ServiceExport resources may associate a WAN policy for the exported service via an annotation. The
SD-WAN policies are defined by a CRD named `WANPolicy.mcw.l7mp.io`.  The format of this CRD is
completely unspecified for now; below is a sample that is enough for the purposes of this note. At
this point the best option seems to be if we make these CRDs cluster-global, to avoid that WAN
policies installed into different namespaces somehow end up conflicting.

```yaml
apiVersion: mcw.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
name: sd-wan-priority-high
spec:
  tunnel: business
  port: 31111
---
apiVersion: mcw.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-low
spec:
  tunnel: default
  port: 31112
---
apiVersion: mcw.l7mp.io/v1alpha1
kind: WANPolicy
metadata:
  name: sd-wan-priority-internet
spec:
  tunnel: ""
  port: 31113
```

In a full design we may write the requested SLAs here, or we may classify on the SNI on the SD-WAN
side, or anything else the SD-WAN will be able to enforce.

### Mechanics

We deconstruct our ServiceImports and ServiceExports to actual Kubernetes resources/objects and use
existing implementations to encode the policies in our EW gateways. WANPolicy resources do not
appear explicitly in any of the gateway pipelines; these resources are purely virtual and serve
only (1) for the user to be able to indicate the chosen WAN policy in service exports and (2) for
the control plane to drive the selection of the destination port in the egress gateway pipeline
(see below).

#### Interconnect fabric: SD-WAN

For each SD-WAN priority level (say, `business` and `default`), we enforce a separate HTTP(s) EW-EW
HTTP(S) session. (They must be separate otherwise if everything goes over a single HTPP(S) stream
then the SD-WAN may not be able to classify the inter-cluster traffic to apply the forwarding
preferences.)

We apply the following rules.
* All SD-WAN priority levels (i.e., tunnels) are wrapped by a WANPolicy CRD that specifies the port
  used for sending traffic over the SD-WAN with that priority (say, 31111 for high-prio traffic).
* All access to the receiver side EW-gateway that is to receive high priority on the SD-WAN uses
  some predefined port X that we specify in the corresponding WANPolicy resource.
* We may define a separate port for traffic that is to be exchanged over the Internet.
* Pod IP ranges are explicitly routed through the SD-WAN: any time a pod from one cluster sends a
  packet to a pod in another cluster, that packer is routed through the SD-WAN.

We assume the SD-WAN is bootstrapped with appropriate per-destination-port application-aware
routing policies and routing table entries as per the above.

#### EW gateways

We use a standard [Kubernetes gateway
implementation](https://gateway-api.sigs.k8s.io/implementations) that supports a sufficiently broad
subset of the Gateway API. Maybe the best choice would be
[Istio](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api): that way we
could use further service-mesh functionality *inside the clusters* as well.

We assume that the EW gateway pods are labeled with `app.kubernetes.io/name: gateway` or whatever
the implementation we choose uses for this purpose. Note further that some Gateway API
implementations automatically wrap all Gateways with a LoadBalancer service to make them available
from outside the cluster. We will have to stop the implementation from doing that otherwise our EW
gateways will be exposed to the Internet, which is definitely not what we want. For instance,
adding the annotation `networking.istio.io/service-type: ClusterIP` will make sure that the
services Istio creates are of type ClusterIP (so not available externally).

##### Ingress EW gateway pipeline

The ingress gateway pipeline contains the necessary Kubernetes resources for the receiving side
cluster to ingest inter-cluster traffic from the SD-WAN for the `payment.secure` service exported
from the cluster.

1. Create an ingress EW gateway for the `payment.secure` service. We assume that the Gateway API
   implementation will *not* expose these Gateway listeners to the Internet. Instead, we create our
   own Services and ServiceExports that make sure that the services are available only *between*
   the clusters, but not from the Internet.

   ```yaml
   apiVersion: gateway.networking.k8s.io/v1beta1
   kind: Gateway
   metadata:
     name: payment-ingress
     namespace: secure
     annotations: 
       networking.istio.io/service-type: ClusterIP
   spec:
     gatewayClassName: whatever
     listeners:
     - name: sd-wan-priority-high
       protocol: HTTP
       port: 31111
       allowedRoutes:
         namespaces:
           from: Same
   ```

   Since the SD-WAN policy associated with the service is `sd-wan-priority-high`, we look up the
   identically names WANPolicy and install the listener at the corresponding port (31111).

1. Create a ServiceExport corresponding to the `payment-ingress.secure` service: this will export
   this service to the rest of the clusters (but not to the Internet).

   ```yaml
   apiVersion: multicluster.k8s.io/v1alpha1
   kind: ServiceExport
   metadata:
     name: mcw-ingress-gateway
     namespace: mcw
   ```

1. Add a
   [HTTPRoute](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRoute)
   to the Gateway, that will route the requests received on the `payment-ingress.secure` Gateway 
   resource, which we attach to the above ingress EW Gateway (`mcw-ingress-gateway`).

   ```yaml
   apiVersion: gateway.networking.k8s.io/v1beta1
   kind: HTTPRoute
   metadata:
     name: payment-ingress
     namespace: secure
   spec:
     parentRefs:
     - name: payment-ingress
       namespace: secure
     hostnames:
       - payment.secure.svc.cluster.local
     rules:
       - filters:
           - urlRewrite:
               hostname: payment.secure.svc.cluster.local
         backendRefs:
           - name: payment.secure.svc.cluster.local
             port: 8080
   ```

   The `http.rules` rewrites the `hostname` to the original name of the target service and sets the
   `backendRefs` to refer to the exported service `payment.secure` over port 8080.

##### Egress EW gateway pipeline

The egress gateway pipeline should contain the necessary Kubernetes resources for the sender side
cluster to send traffic over the SD-WAN to the receiver side for the services that are imported
into the cluster. The nice thing in this model that there is no need to explicitly create anything
on the egress side, since the [Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
implementation will automatically create the necessary ServiceImport in each cluster that will
route queries to the ingress gateway on the exporting side. 

In the above example, the following ServiceImport and EndpointSlice will be created by the MCS API.

```yaml
apiVersion: multicluster.k8s.io/v1alpha1
kind: ServiceImport
metadata:
  name: payment-ingress
  namespace: secure
spec:
  ips:
  - 42.42.42.42
  type: "ClusterSetIP"
  ports:
  - protocol: TCP
    port: 31111
```

Querying the `http://payment-ingress.secure.svc.clusterset.local:31111` will land at the ingress EW
gateway on the exporting side, from where it should be routed to the `payment.secure` service backend.

There are two issues with the default ServiceImport created automatically for us: first, the port
is 31111 whereas the user may want to use the original service port 8080, and second the hostname
also changed from `payment.secure.svc.clusterset.local`. 

In order to address this, or when no MCS implementation is available, we may create a set of
identical dummy Service and Endpoint resources that would look something like the below:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: payment-ingress
  namespace: secure
spec:
  ports:
  - name: payment-secure
    protocol: TCP
    port: 8080
    targetPort: 31111
---
apiVersion: v1
kind: Endpoints
metadata:
  name: payment-ingress
  namespace: secure
subsets:
  - addresses:
      - ip: IP_1
```

Here, the Service must be selector-less, so that we provide our own Endpoint object that backs the
service, and the list the IP address map to the IPs of the ingress EW gateway pods (the IP
addresses of the `payment-ingress.secure` EW gateway in cluster-1 in our case, i.e., `IP_1`).

In order to simplify this, we may create an additional shadow service on the receiving side that
will accept requests on a well-known hostname on the original port.

``` yaml
apiVersion: v1
kind: Service
metadata:
  name: payment-secure
  namespace: mcw
spec:
  type: ExternalName
  externalName: payment-ingress.secure.svc.clusterset.local

```

Note that only those clusters would receive requests from that actually (1)
export the corresponding service and (2) have healthy backend pods for the service.

<!-- ##### Logistics -->

<!-- We bootstrap the egress pipeline with a set of dummy services that will allow routing the requests -->
<!-- to the receiver-side EW gateway(s). In particular, for any remote cluster participating in the -->
<!-- multi-cluster fleet, we create a service whose endpoints contain the externally reachable IP -->
<!-- address of the corresponding EW gateway. -->

<!-- In the running example, we represent the EW gateway of cluster-1 on the sender side with the a -->
<!-- dummy service and the corresponding Endpoint object. -->

<!-- 1. We create a dummy service that will represent the IP address(es) of the EW gateways on the -->
<!--    remote clusters (cluster-1 for now).  Note that the service deliberately has [no -->
<!--    selectors](https://kubernetes.io/docs/concepts/services-networking/service/#services-without-selectors): -->
<!--    in such cases Kubernetes does not create the Endpoint object to back the service, so we can -->
<!--    create one manually and add the IP address of the EW gateways of the target clusters explicitly. -->
   
<!--    ```yaml -->
<!--    apiVersion: v1 -->
<!--    kind: Service -->
<!--    metadata: -->
<!--      name: mcw-cluster-1-target -->
<!--      namespace: mcw -->
<!--    spec: -->
<!--      clusterIP: None -->
<!--      ports: -->
<!--        - protocol: TCP -->
<!--          port: 31111 -->
<!--        - protocol: TCP -->
<!--          port: 31112 -->
<!--    ``` -->
   
<!--    Note that we list all the SD-WAN ports associated with the different SD-WAN priority levels: we -->
<!--    want to reuse the same service across all egress EW gateway policies. -->

<!-- 1. We manually create the Endpoint object for the dummy service and list the IP address of the -->
<!--    Gateway that we want to receive the corresponding traffic (the IP addresses of the EW gateway in -->
<!--    cluster-1 in our case, i.e., `IP_1`). -->

<!--    ```yaml -->
<!--    apiVersion: v1 -->
<!--    kind: Endpoints -->
<!--    metadata: -->
<!--      name: mcw-cluster-1-target -->
<!--      namespace: mcw -->
<!--    subsets: -->
<!--      - addresses: -->
<!--          - ip: IP_1 -->
<!--    ``` -->

<!-- The idea here is that any query sent to the `mcw-cluster-1-target` service will be forwarded to -->
<!-- the EW gateway of cluster-1, and we assume that all participating clusters are wrapped with one -->
<!-- such service/endpoint pair on every other cluster. We will use these dummy services below as -->
<!-- backends for the egress gateway policies. -->

<!-- #### Compiling the ServiceImport -->

<!-- Compiling the ServiceImport to Kubernetes APIs is a tad bit more complex than on the export side, -->
<!-- since we need to apply the L4/L7 rules and the WAN policies in the egress pipeline. The below -->
<!-- resources are created when importing the `payment.secure` service. -->

<!-- 1. We create a shadow service in the `mcw` namespace. The idea is that whenever an application -->
<!--    wants to send a query to the global `payment.secure` service they must use the shadow service -->
<!--    `payment-secure.mcw` instead of the original service. -->
   
<!--    ```yaml -->
<!--    apiVersion: v1 -->
<!--    kind: Service -->
<!--    metadata: -->
<!--      name: payment-secure -->
<!--      namespace: mcw -->
<!--    spec: -->
<!--      selector: -->
<!--        app.kubernetes.io/name: gateway -->
<!--      ports: -->
<!--        - name: http -->
<!--          protocol: TCP -->
<!--          port: 8080 -->
<!--    ``` -->
   
<!--    Note that we set the selector to match the gateway pods: that way, any request to the service -->
<!--    will land at one of our EW gateway pods. -->

<!-- 1. We open a Gateway listener at the target port to receive the query on our EW gateway. -->

<!--    ```yaml -->
<!--    apiVersion: gateway.networking.k8s.io/v1beta1 -->
<!--    kind: Gateway -->
<!--    metadata: -->
<!--      name: payment-secure -->
<!--      namespace: mcw -->
<!--    spec: -->
<!--      gatewayClassName: whatever -->
<!--      listeners: -->
<!--      - name: payment-secure-egress-gateway -->
<!--        protocol: HTTP -->
<!--        port: 8080 -->
<!--        allowedRoutes: -->
<!--          namespaces: -->
<!--            from: Same -->
<!--    ``` -->

<!--    An actual implementation will not create a separate Gateway per service-import but it will -->
<!--    rather just add another HTTP listener to a global `egress-http-gateway` instance, but for the -->
<!--    purpose of this spec it is easier to explain what's going on this way. -->

<!-- 1. We add a HTTPRoute that represents the L7 rules to be applied on the sender side and to send the -->
<!--    query over to the other side. For that purpose, we will use a `backendRef` to the dummy service -->
<!--    created in the bootstrapping step to direct the matching packets to the receiver-side EW -->
<!--    gateway. -->

<!--    ```yaml -->
<!--    apiVersion: gateway.networking.k8s.io/v1beta1 -->
<!--    kind: HTTPRoute -->
<!--    metadata: -->
<!--      name: payment-secure -->
<!--      namespace: mcw -->
<!--    spec: -->
<!--      parentRefs: -->
<!--      - name: payment-secure -->
<!--      hostnames: -->
<!--        - payment-secure.mcw.svc.cluster.local -->
<!--        - payment.secure.svc.cluster.local -->
<!--      rules: -->
<!--       - matches: -->
<!--           - method: GET -->
<!--             path: -->
<!--               type: PathPrefix -->
<!--               value: /payment -->
<!--         filters: -->
<!--           - urlRewrite: -->
<!--               hostname: payment.secure.svc.cluster.local -->
<!--         backendRefs: -->
<!--           - name: mcw-cluster-1-target -->
<!--             namespace: mcw -->
<!--             port: 31111 -->
<!--             weight: 1 -->
<!--       - matches: -->
<!--           - path: -->
<!--               type: PathPrefix -->
<!--               value: /stats -->
<!--         filters: -->
<!--           - urlRewrite: -->
<!--               hostname: payment.secure.svc.cluster.local -->
<!--         backendRefs: -->
<!--           - name: mcw-cluster-1-target -->
<!--             namespace: mcw -->
<!--             port: 31112 -->
<!--             weight: 1 -->
<!--      ``` -->

<!--    Note that we listen to both the original `payment.secure` hostname as well as to that of the -->
<!--    shadow service `payment-secure.mcw` for safety. Note further that the `backendRef` of the rule -->
<!--    points to the dummy service for the ingress EW gateway of cluster-1, the target port is the one -->
<!--    that belongs to the selected SD-WAN priority, and finally the weight is set to 1.  Here, the -->
<!--    ServiceExport specifies the `sd-wan-priority-high` WAN policy for the `payment.secure/payment` -->
<!--    API endpoint so we select the corresponding port from the matching WANPolicy, i.e., 31111. The -->
<!--    API endpoint `payment.secure/stats` is confined to the low-prio tunnel, so it gets the port from -->
<!--    the corresponding WANPolicy (31112).  In addition, the weight allows us to load-balance requests -->
<!--    across the receiver side clusters based on the number of pods allocated in each cluster; e.g., -->
<!--    if cluster-X has 3 pods for the `payment.secure` service and cluster-Y has 4 pods, then the -->
<!--    corresponding weights will be as follows. -->

<!--    ```yaml -->
<!--    ... -->
<!--      backendRefs: -->
<!--        - name: mcw-cluster-X-target -->
<!--          namespace: mcw -->
<!--          port: 31111 -->
<!--          weight: 3 -->
<!--        - name: mcw-cluster-Y-target -->
<!--          namespace: mcw -->
<!--          port: 31111 -->
<!--          weight: 4 -->
<!--    ``` -->

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
