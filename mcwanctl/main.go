package main

import (
	"context"
	"log"
	"os"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	"k8s.io/client-go/tools/clientcmd"

	versionedclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	//"k8s.io/apimachinery/pkg/types"
	//"k8s.io/client-go/kubernetes"
	//tst "istio.io/client-go/pkg/applyconfiguration/networking/v1beta1"
	//istioversionedclient "istio.io/client-go/pkg/clientset/versioned"
	// gatewayv1beta1 "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1beta1"
)

func main() {

	kubeconfig := os.Getenv("KUBECONFIG")
	namespace := os.Getenv("NAMESPACE")

	if len(kubeconfig) == 0 || len(namespace) == 0 {
		log.Fatalf("Environment variables KUBECONFIG and NAMESPACE need to be set")
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Failed to create k8s rest client: %s", err)
	}

	ic, err := versionedclient.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("Failed to create istio client: %s", err)
	}


	GW := &v1beta1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gateway",
		},
		Spec: v1beta1.GatewaySpec{
			GatewayClassName: v1beta1.ObjectName("istio"),
			Listeners: []v1beta1.Listener{{
				Name: v1beta1.SectionName("listener8000"),
				Port: v1beta1.PortNumber(8000),
				Protocol: v1beta1.ProtocolType("http"),
			}},
		},
	}

	_, err = ic.GatewayV1beta1().Gateways(namespace).Create(context.TODO(), GW, metav1.CreateOptions{})
	if err != nil {
		log.Fatalf("Failed to create gateway: %s", err)
	}


	gwList, err := ic.GatewayV1beta1().Gateways(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Fatalf("Failed to get Gateway in %s namespace: %s", namespace, err)
	}


	for i := range gwList.Items {
		gw := gwList.Items[i]
		fmt.Println(gw)
	}



}
