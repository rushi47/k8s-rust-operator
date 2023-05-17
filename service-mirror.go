package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	// Set up logger .
	log := &logrus.Logger{
		Out:   os.Stderr,
		Level: logrus.DebugLevel,
		Formatter: &logrus.TextFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
			ForceColors:     true,
			DisableColors:   false,
			FullTimestamp:   true,
		},
	}
	//Shows line number: Too long
	// log.SetReportCaller(true)

	log.Info("-- Starting Global Mirror ---")

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Panicln("Probably running Inside Cluster", err.Error())
	}

	// creates the clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Panicln("Issue in building client from config", err.Error())
	}

	/* Get running services in all the names, labelled with "mirror.linkerd.io/mirrored-service: true"
	and which service which does have label : mirror.linkerd.io/headless-mirror-svc-name. Build selector for it.
	*/
	mirrorSvcLabel, err := labels.NewRequirement("mirror.linkerd.io/mirrored-service", selection.Equals, []string{"true"})
	if err != nil {
		log.Errorln("Unable to generate error requirement", "Err", err.Error())
	}

	// Create label requirements for "mirror.linkerd.io/headless-mirror-svc-name" not existing
	mirrorSvcParentLabel, err := labels.NewRequirement("mirror.linkerd.io/headless-mirror-svc-name", selection.DoesNotExist, []string{})
	if err != nil {
		log.Errorln("Unable to generate error requirement", "Err", err.Error())
	}

	// Create the label selector
	svcFilter := labels.NewSelector()
	svcFilter = svcFilter.Add(*mirrorSvcParentLabel)
	svcFilter = svcFilter.Add(*mirrorSvcLabel)

	mirroredServices, err := client.CoreV1().Services("").List(context.Background(), metav1.ListOptions{LabelSelector: svcFilter.String()})

	if err != nil {
		log.Errorln("Unable to get running services for filter", "filter : ", svcFilter.String(), "Err : \n", err.Error())
	}

	//Map of Mirrored services with targetCluster as key and responding service
	mirroredService := make(map[string][]corev1.Service)
	log.Debugln("All running Services :")
	for _, svc := range mirroredServices.Items {
		//Only gather service which doesnt have label mirror.linkerd.io/headless-mirror-svc-name, as this are the parent services
		targetClusterame := svc.GetLabels()["mirror.linkerd.io/cluster-name"]
		if _, ok := mirroredService[targetClusterame]; !ok {
			mirroredService[targetClusterame] = []corev1.Service{}
		}
		mirroredService[targetClusterame] = append(mirroredService[targetClusterame], svc)

	}

	//Iterate over map of targetCluster -> headlessService and check if global service exists.
	for tgCluster, mirrorService := range mirroredService {
		log.Debug("Key=", tgCluster)
		for _, hdlSvc := range mirrorService {
			hdlSvc = *hdlSvc.DeepCopy()
			//Check if global/aggregator service exists
			log.Debug("Svc from cluster = ", hdlSvc.Name)
			globalSvcName := strings.Split(hdlSvc.Name, fmt.Sprintf("-%s", tgCluster))[0]
			globalSvcName = globalSvcName + "-global"
			log.Info("Global svc Name = ", globalSvcName)
			_, err := client.CoreV1().Services("").Get(context.Background(), globalSvcName, metav1.GetOptions{})

			//If global service doesnt exist cerate it
			if !apiError.IsAlreadyExists(err) {

				/*
					- Spin up the new global service with cardinal index as x-global,
					Which will be aggregator for mirrored services from target cluster x-target0, x-target1
				*/

				svcMeta := &metav1.ObjectMeta{
					Name:      globalSvcName,
					Namespace: hdlSvc.Namespace,
					Labels: map[string]string{
						"mirror.linkerd.io/global-mirror-for": tgCluster,
					},
				}

				svcSpec := &corev1.ServiceSpec{
					ClusterIP: "None",
					Ports:     hdlSvc.Spec.Ports,
				}
				globalService := &corev1.Service{
					ObjectMeta: *svcMeta,
					Spec:       *svcSpec,
				}
				//Create clientSet to create Service
				defaultCreateOptions := metav1.CreateOptions{}
				globalSvc, err := client.CoreV1().Services(hdlSvc.Namespace).Create(context.Background(), globalService, defaultCreateOptions)
				if !apiError.IsAlreadyExists(err) {
					log.Info("Unable to create Service with", "name=", globalSvcName, "err=", err)
				}
				log.Info("Global Service Create", "svc=", globalSvc.Name)

			} else {
				log.Error("Unable to get the global Service", "Name=", globalSvcName)
			}

		}
	}
}
