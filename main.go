package main

import (
	"context"
	"flag"
	"fmt"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/util/logs"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/version"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/ingress-nginx/pkg/apis/ingressgroup/v1"
	igclient "k8s.io/ingress-nginx/pkg/client/clientset/versioned"
	inggroupInformers "k8s.io/ingress-nginx/pkg/client/informers/externalversions"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/version/verflag"
	"os"
	"time"
)

type OperatorManagerServer struct {
	Master     string
	Kubeconfig string
}

func NewOMServer() *OperatorManagerServer {
	s := OperatorManagerServer{}
	return &s
}

func main() {
	s := NewOMServer()
	flag.StringVar(&s.Master, "master", s.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	flag.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig, "Path to kubeconfig file with authorization and master location information.")

	flag.Parse()

	logs.InitLogs()
	defer logs.FlushLogs()

	verflag.PrintAndExitIfRequested()

	if err := Run(s); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

}

func Run(s *OperatorManagerServer) error {
	// To help debugging, immediately log version
	klog.Infof("Version: %+v", version.Get())

	_, extensionCRClient, kubeconfig, err := createClients(s)
	//kubeClient, leaderElectionClient, _, kubeconfig, err := createClients(s)

	if err != nil {
		return err
	}

	err = CreateIngressGroupCRD(extensionCRClient)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			klog.Infof("redis cluster crd is already created.")
		} else {
			fmt.Fprint(os.Stderr, err)
			return err
		}
	}

	versionedClient, err := igclient.NewForConfig(kubeconfig)
	if err != nil {
		klog.Fatal(err)
	}

	sharedInformers := inggroupInformers.NewSharedInformerFactory(versionedClient, time.Duration(0)*time.Second)

	ctx := context.TODO()
	stopCh := ctx.Done()

	//watch ingress group
	ingGroupEventHandler := cache.ResourceEventHandlerFuncs{
		//create ingress group
		AddFunc: func(obj interface{}) {
			addIngGroup := obj.(*v1.IngressGroup)
			klog.Warningf("addIngGroup: %v/%v", addIngGroup.Namespace, addIngGroup.Name)
		},
		//delete ingress group
		DeleteFunc: func(obj interface{}) {
			delIngGroup, _ := obj.(*v1.IngressGroup)
			klog.Warningf("delIngGroup: %v/%v", delIngGroup.Namespace, delIngGroup.Name)
		},
		//update ingress group
		UpdateFunc: func(old, cur interface{}) {
			oldIngGroup := old.(*v1.IngressGroup)
			curIngGroup := cur.(*v1.IngressGroup)
			klog.Warningf("oldIngGroup: %v/%v ; curIngGroup: %v/%v", oldIngGroup.Namespace, oldIngGroup.Name, curIngGroup.Namespace, curIngGroup.Name)
		},
	}

	sharedInformers.Cr().V1().IngressGroups().Informer().AddEventHandler(ingGroupEventHandler)

	sharedInformers.Start(stopCh)

	<-stopCh
	return fmt.Errorf("unreachable")
}

func createClients(s *OperatorManagerServer) (*clientset.Clientset, *extensionsclient.Clientset, *restclient.Config, error) {
	kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
	if err != nil {
		return nil, nil, nil, err
	}

	kubeconfig.QPS = 100
	kubeconfig.Burst = 100

	kubeClient, err := clientset.NewForConfig(restclient.AddUserAgent(kubeconfig, "operator-manager"))
	if err != nil {
		klog.Fatalf("Invalid API configuration: %v", err)
	}

	extensionClient, err := extensionsclient.NewForConfig(restclient.AddUserAgent(kubeconfig, "operator-manager"))
	if err != nil {
		klog.Fatalf("Invalid API configuration: %v", err)
	}

	return kubeClient, extensionClient, kubeconfig, nil
}

func CreateIngressGroupCRD(extensionCRClient *extensionsclient.Clientset) error {
	crd := &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ingressgroups." + v1.SchemeGroupVersion.Group,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: v1.SchemeGroupVersion.Group,
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					// Served is a flag enabling/disabling this version from being served via REST APIs
					Served: true,
					Name:   v1.SchemeGroupVersion.Version,
					// Storage flags the version as storage version. There must be exactly one flagged as storage version
					Storage: true,
				},
			},
			Scope: v1beta1.NamespaceScoped,
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind:       "IngressGroup",
				ListKind:   "IngressGroupList",
				Plural:     "ingressgroups",
				Singular:   "ingressgroup",
				ShortNames: []string{"ig"},
				Categories: []string{"all"},
			},
			Validation: &v1beta1.CustomResourceValidation{
				OpenAPIV3Schema: &v1beta1.JSONSchemaProps{
					Properties: map[string]v1beta1.JSONSchemaProps{
						"spec": {
							Properties: map[string]v1beta1.JSONSchemaProps{
								"services": {
									Type: "array",
									Items: &v1beta1.JSONSchemaPropsOrArray{
										Schema: &v1beta1.JSONSchemaProps{
											Type:     "object",
											Required: []string{"name", "namespace"},
											Properties: map[string]v1beta1.JSONSchemaProps{
												"name": {
													Type: "string",
												},
												"namespace": {
													Type: "string",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	_, err := extensionCRClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	return err
}
