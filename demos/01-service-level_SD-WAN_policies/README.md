# Service-level SD-WAN policies

## User Story

The `payment-secure` HTTP service (port 8000), deployed into cluster-2, serves sensitive data, so the service owner in cluster-2 wants to secure access to this service, by forcing all queries/responses to this service to be sent over the SD-WAN interconnect in the fastest and most secure way. At the same time, the
`payment-insecure` service serves less-sensitive bulk traffic (e.g., logging) , so the corresponding traffic exchange defaults to the Internet.

## Setup

We have two k8s clusters interconnected with SD-WAN.

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

Both clusters are installed with:
- k8s (v1.25)
- Istio (v1.16.0)

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

*TODO*

### Testing and Demo

6. Create a client deployment on **cluster1** with [yaml/0-net-debug.yaml](yaml/0-net-debug.yaml)

```console
kubectl apply -f yaml/0-net-debug.yaml
```

7. Send a request to service running in cluster2 from the client (`net-debug` pod) in **cluster1**:

```console
kubectl exec -it $(kubectl get pods -o custom-columns=":metadata.name" | grep net-debug-nonhost) -- curl -v -I -H "Host: payment-secure.default.svc.clusterset.local" http://payment-secure.default.svc.cluster.local:8000
```

8. Do a volumetric measurement to check proper SD-WAN tunneling:

- Generate test traffic
```console
while [ true ]; do kubectl exec -it $(kubectl get pods -o custom-columns=":metadata.name" | grep net-debug-nonhost) -- curl -sS -H "Host: payment-secure.default.svc.clusterset.local" http://payment-secure.default.svc.cluster.local:8000 -o /dev/null; done
```
- Observe metrics on the vManage UI
