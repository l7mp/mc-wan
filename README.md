# A Multi-cluster service mesh east-west gateway for SD-WAN

The goal of this project is to build an east-west (EW) gateway to interconnect two or more
service-mesh islands over an SD-WAN and integrate the L7-preferences on the service-mesh side with
the L3/L4 traffic management policies on the SD-WAN interconnect, as well as the observability and
security functions

## Means

* Services between clusters are exported/imported using the [Multi-cluster Services API
  (KEP-1645)](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
  CRDs, extended with L7 policies and SD-WAN-related semantics to control the way traffic egresses
  from, and ingresses into, a cluster.
* The import/export policies are rendered into regular [Kubernetes Gateway
  API](https://gateway-api.sigs.k8s.io) CRDs and implemented on top of a standard Kubernetes
  gateway implementing the API.
* The EW gateways exchange inter-cluster traffic in a way so as to the SD-WAN fabric
  interconnecting the clusters can make meaningful classification of traffic to/from different
  services and deliver the required service level to each service as specified by the operator.

# User stories

* **Receiver-controlled default SD-WAN policies.** The `payment.secure` HTTP service (port 8080),
deployed into cluster-1, serves sensitive data, so the service owner in cluster-1 wants to secure
access to this service, by forcing all queries/responses to this service to be sent over the SD-WAN
interconnect in the fastest and most secure way. At the same time, the `logging.insecure` service
serves less-sensitive bulk traffic, so the corresponding traffic exchange defaults to the Internet.

* **Ingress L7 traffic management on the receiver side.** Same scenario as above, but now the only
GET queries to the `http://payment.secure:8080/payment` API endpoint are considered sensitive,
access to `http://payment.secure:8080/stats` are irrelevant, and any other access to the service is
denied.

* **Egress L7 traffic management rules on the sender side.** Same scenario as above, but now all
queries sent between clusters must include an HTTP header field called `sender-cluster` to indicate
the sending cluster's identity to the service backends on the receiver side. In addition, the
sender can also overrides the service-owner's default SD-WAN policies if they want.

* **Resiliency:** Regular Internet connection to one of the clusters goes down. The EW gateways
automatically initiate a circuit-breaking for the failed EW-gateway endpoint and retry all failed
connection over either the SD-WAN or another cluster where healthy backends are still running.

* **Monitoring:** Task operators want end-to-end monitoring of queries across the
clusters. EW-gateways add a `spanid` header to all HTTP(S) traffic exchanged over the SD-WAN
interconnect.

## Concepts

We use HTTP throughout for simplicity. It is trivial to enforce encryption by rewriting all rules
to HTTPS.

### Interconnect fabric: SD-WAN

For each SD-WAN priority level (say, `business` and `default`), we enforce a separate HTTP(s) EW-EW
HTTP(S) session, otherwise if everything goes over a single HTPP(S) stream then the SD-WAN may not
be able to classify our traffic to apply the forwarding preferences.

**Idea:** we apply the following rule:
* all access to the receiver side EW-gateway that is to receive high priority on the SD-WAN uses
  some predefined port X (say, 31111),
* the rest of the priority levels use the subsequent ports X+1, X+2, etc.,
* we can define a separate port for traffic that is to be exchanged over the Internet.

We assume the SD-WAN is bootstrapped with appropriate per-destination port application-aware
routing policies to enforce the priority encoded in the destination port.

### EW gateways

We use a standard implementation that supports a sufficiently large subset of the Gateway API, see
[here](https://gateway-api.sigs.k8s.io/implementations) for an up-to-date list. Maybe the best
choice would be
[Istio](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api): that way we
could use further service-mesh functionality as well.

We assume that the EW gateway pods are labeled with `app.kubernetes.io/name: gateway` or whatever
the implementation we choose use for this purpose.

## Service export

In order to allow access from other clusters, a service has to be explicitly exported from the
hosting cluster. 

### CRD

We introduce a `ServiceExport.mc-wan.l7mp.io` CRD for controlling exported services. Our
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
          name: sd-wan-priority-high
      - matches:
          - path:
              type: PathPrefix
              value: /stats
        backendRefs:
          name: sd-wan-priority-low
```

Note that the name of the ServiceExport is the same as that of the service to be exported and
`rules` is a list of standard
[`HTTPRouteRule`](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRouteRule)
objects from the Kubernetes Gateway API. Each rule can specify a `backendRef` to the
`sd-wan-priority-high` and `sd-wan-priority-high` services: these are dummy services we create to
represent the SD-WAN priority for the queries. Note that we cannot enforce these priorities on the
receiver side (by the time we receive the request on the EW gateway it has already passed the
SD-WAN), so these priorities serve only as a default priority to the sender side (unless they decide to
override the default priority).

### Egress gateway logistics

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

### Compiling the ServiceExport

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

The `rules` is copied verbatim to the HTTPRoute, except that we rewrite the `hostname` to the
original name of the target service and the set the `backendRefs` to refer to the exported service
`payment.secure` over port 8080. 

We may need to add an [annotation](https://gateway-api.sigs.k8s.io/guides/multiple-ns) or a
`PolicyTargetReference` as well to allow cross-namespace routing.
    
## Service import

In order to access an exported service, the sender-side cluster must explicitly import the target
service.

### CRDs

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
  rules:
    - filter:
        requestHeaderModifier:
          add:
            name: origin-cluster
            value: cluster-2
```

Note that the name of the ServiceImport is the same as that of the service to be imported. In
addition, `ports` is a standard `ServicePort` object from the [Multi-cluster Services
API](https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api)
SerrviceImport CRDs, and `rules` is a list of standard
[`HTTPRouteRule`](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1beta1.HTTPRouteRule)
objects from the Kubernetes Gateway API. Each rule can specify a `backendRef` to the dummy
`sd-wan-priority-high` and `sd-wan-priority-high` services to override the SD-WAN priority set by
the service owner on the receiver side.

### Compiling the ServiceImport

Compiling the ServiceImport to Kubernetes APIs is a tad bit more complex than on the export
side. The below resources are created for the above sample service import. 

1. We create a shadow service in the `mc-wan` namespace. The idea is that whenever an application
   wants to send a query to the global service they must use the shadow service instead of the
   original service name, with the port specified in the ServiceImport (if no port is specified in
   the ServiceImport then we fall back to the original port on the receiver side).
   
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
   rather just add another HTTP listener to a global `egress-gateway`, but for the purpose of this
   spec it is easier to explain what's going on this way.

1. We add a HTTPRoute that represents the L7 rules to be applied on the sender side and to send the
   query over to the other side. For that purpose, we will use a target service that we create in
   order to forward the request to the EW gateways that actually export the service (see below).

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
           - name: payment-secure-target
             namespace: mc-wan
             port: 31111
   ```

   Note that listen to the hostname of both the original `payment.secure` service as well as for
   that of the shadow service `payment-secure.mcw`. Note further that the target port is the one
   that belongs to the selected SD-WAN priority.

1. We still need the service that we used above as a `backendRef`. The goal is to route traffic to
   this service to one of the EW gateways in the clusters that export the service. We create a
   dummy service that will represent the IP addresses of the appropriate EW gateways.  Note that
   the service deliberately has [no
   selectors](https://kubernetes.io/docs/concepts/services-networking/service/#services-without-selectors):
   in such cases Kubernetes does not create an Endpoint object to back the service, so we can
   create one manually and add the IP addresses of the EW gateways of the target clusters
   explicitly.
   
   ```yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: payment-secure-target
     namespace: mc-wan
   spec:
     ports:
       - protocol: TCP
         port: 31111
   ---
   apiVersion: v1
   kind: Endpoints
   metadata:
     name: payment-secure-target
     namespace: mc-wan
   subsets:
     - addresses:
         - ip: IP_1
       ports:
         - port: 31111
           port: 31112
           port: 31113
   ```

   In this example only cluster-1 exports the service, so we add the IP address of the
   corresponding EW gateway. This is most probably the IP of one of the nodes because, recall, we
   expose EW gateways with NodePort services so that we can control routing and force traffic
   through the SD-WAN. Note further that we have added all SD-WAN ports to the service, because we
   want to reuse the same service even there are multiple HTTP filter rules in the ServiceImport
   (e.g., there can be a rule that only `http://payment.secure/payment` is sensitive and
   `http://payment.secure/logging` is not and then we will have different HTTPRoute rules with the
   same `backendRef` pointing to this service.

