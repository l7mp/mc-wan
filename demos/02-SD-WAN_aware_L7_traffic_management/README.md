# SD-WAN aware L7 traffic management

## User Story

Same scenario as [Service-level SD-WAN policies](../01-service-level_SD-WAN_policies/README.md), but now only GET queries to the http://payment.secure:8080/payment API endpoint are considered sensitive, access to http://payment.secure:8080/stats is irrelevant (defaults to the Internet), and any other access to the service is denied.

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

0. Follow installation steps of the [Service-level SD-WAN policies](../01-service-level_SD-WAN_policies/README.md) demo.

2. Appy client side L7 access control with [yaml/5-client-side-l7-policy-gw.yaml](yaml/5-client-side-l7-policy-gw.yaml). This config restricts access to the path `/anything`.

```console
kubectl apply -f yaml/5-server-side-l7-policy.yaml
```

*TODO*

### Test and Demo

*TODO*
