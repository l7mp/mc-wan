# Service-level SD-WAN policies

## User Story

The `payment-secure` HTTP service (port 8000), deployed into **cluster2**, serves sensitive data, so the service owner in **cluster2** wants to secure access to this service, by forcing all queries/responses to this service to be sent over the SD-WAN interconnect in the fastest and most secure way. At the same time, the `payment-insecure` service serves less-sensitive bulk traffic (*e.g.*, logging) , so the corresponding traffic exchange defaults to the Internet.

## Setup

We have two k8s clusters interconnected with SD-WAN.

The SD-WAN is configured with two SLA class: *Business Internet* (fastest and most secure) and *Public Internet* (best-effort).

The clusters are installed with k8s (v1.25) and Istio (v1.16.0).

```
  +------------SD-WAN-------------+
  |  vEdge ------------- vEdge    |
  +----+-------------------+------+
       |                   |
 +-----+------+      +-----+------+
 |  cluster1  |      |  cluster2  |
 |    k8s     |      |    k8s     |
 +------------+      +------------+
```

## Steps

### Install

0. Install [gateway CRDs](https://gateway-api.sigs.k8s.io/guides/):

```console
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v0.5.1/standard-install.yaml
```

1. Create a dummy `payment` service on **cluster2** using the [yaml file](yaml/1-payment-svc.yaml). In this demo we deploy [httpbin](https://httpbin.org/).

```console
kubectl apply -f yaml/1-payment-svc.yaml
```

2. Create a gateway on **cluster2** with the [yaml/2-gw.yaml](yaml/2-gw.yaml). The configuration considers a private cluster with a single node.

```console
kubectl apply -f yaml/2-gw.yaml
```

3. Define HTTP routes on **cluster2** with the [yaml/3-http-route.yaml](yaml/3-http-route.yaml).

```console
kubectl apply -f yaml/3-http-route.yaml
```

4. Create the shadow service on **cluster1** using the [yaml/4-shadow-svc.yaml](yaml/4-shadow-svc.yaml). Note: the Endpoint `subsets.addresses.ip` field holds the IP address of the node in the other cluster.

```console
kubectl apply -f yaml/4-shadow-svc.yaml
```

5. Configure SD-WAN policies

A simple port-based configuration is sufficient. In this demo the traffic on port 31111 goes to business internet, while port 31112 goes on public internet. This is in sync with the [gateway configuration](yaml/2-gw.yaml). An example configuration:

```
viptela-policy:policy
 app-route-policy _vpn_cnwan_policies_mc-wan
  vpn-list vpn_cnwan_policies
    sequence 1
     match
      source-port 31111
      source-ip 0.0.0.0/0
     !
     action
      backup-sla-preferred-color biz-internet
     !
    !
    sequence 11
     match
      destination-port 31111
      source-ip 0.0.0.0/0
     !
     action
      backup-sla-preferred-color biz-internet
     !
    !
    sequence 21
     match
      source-port 31112
      source-ip 0.0.0.0/0
     !
     action
      backup-sla-preferred-color public-internet
     !
    !
    sequence 31
     match
      destination-port 31112
      source-ip 0.0.0.0/0
     !
     action
      backup-sla-preferred-color public-internet
     !
    !
 !
 lists
  site-list sites_cnwan_policies
   site-id 100
   site-id 111
  !
  vpn-list vpn_cnwan_policies
   vpn 1
  !
 !
!
apply-policy
 site-list sites_cnwan_policies
  app-route-policy _vpn_cnwan_policies_mc-wan
 !
!
```

### Test and Demo

6. Deploy a client into **cluster1** with [yaml/0-net-debug.yaml](yaml/0-net-debug.yaml). In this demo we use curl in [net-debug](https://github.com/l7mp/net-debug).

```console
kubectl apply -f yaml/0-net-debug.yaml
```

7. Send a request to service running in cluster2 from the client (`net-debug` pod) in **cluster1**:

```console
kubectl exec -it $(kubectl get pods -o custom-columns=":metadata.name" | grep net-debug-nonhost) -- curl -v -I -H "Host: payment-secure.default.svc.clusterset.local" http://payment-secure.default.svc.cluster.local:8000
```

8. Do a volumetric measurement to check proper SD-WAN configuration:

- Generate test traffic on *cluster1*:
```console
while [ true ]; do kubectl exec -it $(kubectl get pods -o custom-columns=":metadata.name" | grep net-debug-nonhost) -- curl -sS -H "Host: payment-secure.default.svc.clusterset.local" http://payment-secure.default.svc.cluster.local:8000 -o /dev/null; done
```

- Observe metrics on the vManage UI:
Navigate to *Monitor/Network*, and select a vEdge instance. Click on *Interface*, then select *Real Time* over the graph.

In case of `payment-secure`, We expect to see traffic on *Business Internet*.

For `payment-insecure`, repeat these steps, but generate traffic as:
```console
while [ true ]; do kubectl exec -it $(kubectl get pods -o custom-columns=":metadata.name" | grep net-debug-nonhost) -- curl -sS -H "Host: payment-insecure.default.svc.clusterset.local" http://payment-insecure.default.svc.cluster.local:8000 -o /dev/null; done
```
