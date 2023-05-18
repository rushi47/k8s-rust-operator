package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Make sure all global services lies in only one namespace.
const GLOBAL_SVC_NAMESPACE = "default"

func getServiceFilter(ctx Context) labels.Selector {
	/* Get running services in all the names, labelled with "mirror.linkerd.io/mirrored-service: true"
	and which service which does have label : mirror.linkerd.io/headless-mirror-svc-name. Build selector for it.
	*/
	mirrorSvcLabel, err := labels.NewRequirement("mirror.linkerd.io/mirrored-service", selection.Equals, []string{"true"})
	if err != nil {
		ctx.log.Errorln("Unable to generate error requirement", "Err", err.Error())
	}

	// Create label requirements for "mirror.linkerd.io/headless-mirror-svc-name" not existing
	mirrorSvcParentLabel, err := labels.NewRequirement("mirror.linkerd.io/headless-mirror-svc-name", selection.DoesNotExist, []string{})
	if err != nil {
		ctx.log.Errorln("Unable to generate error requirement", "Err", err.Error())
	}

	// Create the label selector
	svcFilter := labels.NewSelector()
	svcFilter = svcFilter.Add(*mirrorSvcParentLabel)
	svcFilter = svcFilter.Add(*mirrorSvcLabel)

	return svcFilter
}

// Function to handle service additions
func handleServiceAdd(ctx *Context, obj interface{}) {
	/*
		- Check if Global Service is added wrt current service.
		- Global service name of svc: x-target will be x-global.

	*/
	log := ctx.log
	client := ctx.client

	service, ok := obj.(*corev1.Service)
	if !ok {
		log.Errorln("Failed to cast object to Service type")
		return
	}
	log.Info("New Target Service Added ", "svcName= ", service.Name)

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]

	targetSvc := service.DeepCopy()

	// Check if global/aggregator service exists.
	// We dont have this object in Local Cache so check directly.
	// TO DO: Add global object in local cache.
	globalSvcName := strings.Split(targetSvc.Name, fmt.Sprintf("-%s", targetClusterame))[0]
	globalSvcName = globalSvcName + "-global"
	log.Info("Checking global Svc Named = ", globalSvcName, " if exists")
	globalSvc, err := client.CoreV1().Services(GLOBAL_SVC_NAMESPACE).Get(context.Background(), globalSvcName, metav1.GetOptions{})

	//If global service doesnt exist cerate it
	if err != nil && !apiError.IsAlreadyExists(err) {

		/*
			- Spin up the new global service with cardinal index as x-global,
			Which will be aggregator for mirrored services from targetSvc. cluster x-targetSvc.0, x-targetSvc.1
			Global service will always be created in Default.
		*/
		log.Info("New Global Service will be created.", "Name=", globalSvcName)

		svcMeta := &metav1.ObjectMeta{
			Name:      globalSvcName,
			Namespace: targetSvc.Namespace,
			Labels: map[string]string{
				"mirror.linkerd.io/global-mirror-for": targetClusterame,
			},
		}

		svcSpec := &corev1.ServiceSpec{
			ClusterIP: "None",
			Ports:     targetSvc.Spec.Ports,
		}
		globalService := &corev1.Service{
			ObjectMeta: *svcMeta,
			Spec:       *svcSpec,
		}
		//Create clientSet to create Service,
		defaultCreateOptions := metav1.CreateOptions{}
		_, err := client.CoreV1().Services(GLOBAL_SVC_NAMESPACE).Create(context.Background(), globalService, defaultCreateOptions)
		if !apiError.IsAlreadyExists(err) && err != nil {
			log.Error("Issue with service creation", "name=", globalSvcName)
			log.Error(err)
			return
		}

	} else {
		// If service already exists, check if the port from the target service are inside global svc.
		log.Debug("Skipping Creation of New Global Service, Named= ", globalSvcName, " already exists")
		log.Info("Checking if the Spec is synced. [Currently only checks for ports.]")
		targetSvcPort := targetSvc.Spec.Ports
		globalSvcPort := globalSvc.Spec.Ports
		if !reflect.DeepEqual(targetSvcPort, globalSvcPort) {
			//Check port by port, to make sure there is no change.
			//Create map to do efficient checking
			mapPort := make(map[corev1.ServicePort]corev1.ServicePort)
			for _, port := range globalSvcPort {
				port.Name = ""
				mapPort[port] = corev1.ServicePort{}
				mapPort[port] = port
			}

			for _, port := range targetSvcPort {
				if _, ok := mapPort[port]; !ok {
					log.Info("Port= ", port, " doesn't exist in global. Adding.")
					globalSvcPort = append(globalSvcPort, *port.DeepCopy())
				}
			}

			//Again make sure there is change in ports if there is change, update the ports.
			//Checking now it should return true.
			if !reflect.DeepEqual(globalSvcPort, globalSvc.Spec.Ports) {
				//Only update ports.
				log.Debug("Looks like there is difference in Ports ", "NewPorts= ", globalSvcPort, " ExistingPorts=", globalSvc.Spec.Ports, " syncing config.")

				defaultCreateOptions := metav1.CreateOptions{}

				for i, p := range globalSvcPort {
					//Create port name with index as API doenst allow to update port when there are multiple ones.
					p.Name = "port-" + fmt.Sprint(i)
					globalSvcPort[i] = p
				}

				//Update all the ports, cause its addition.
				globalSvc.Spec.Ports = globalSvcPort
				_, err := client.CoreV1().Services(GLOBAL_SVC_NAMESPACE).Update(ctx.ctx, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
				if err != nil {
					log.Error("Unable to update ports, for global service ", "name=", globalSvcName)
					log.Error(err)
					return
				}
			}
		}

	}

	log.Info("Global Service with Name: ", globalSvcName, " should exist.")
}

// Function to handle service updates
func handleServiceUpdate(ctx *Context, oldObj, newObj interface{}) {
	//If nothing has changed, return. https://github.com/kubernetes/client-go/issues/529
	if reflect.DeepEqual(oldObj, newObj) {
		ctx.log.Debug("Nothing has changed in object, skipping update.")
		return
	}

	oldService, ok := oldObj.(*corev1.Service)
	if !ok {
		ctx.log.Errorln("Failed to cast old object to Service type")
		return
	}
	newService, ok := newObj.(*corev1.Service)

	if !ok {
		ctx.log.Errorln("Failed to cast new object to Service type")
		return
	}

	//Get target clusters.
	targetClusterame := newService.GetLabels()["mirror.linkerd.io/cluster-name"]

	//Just handle ports in update for now.
	if !reflect.DeepEqual(oldService.Spec.Ports, newService.Spec.Ports) {

		targetSvcPort := newService.Spec.Ports

		// Assuming global/aggregator service exists.
		// TO DO: Also add logic to handle
		log := ctx.log
		client := ctx.client
		globalSvcName := strings.Split(newService.Name, fmt.Sprintf("-%s", targetClusterame))[0]
		globalSvcName = globalSvcName + "-global"

		log.Info("Checking global Svc Named = ", globalSvcName, " if exists")

		globalSvc, err := client.CoreV1().Services("default").Get(context.Background(), globalSvcName, metav1.GetOptions{})

		if !apiError.IsAlreadyExists(err) && err != nil {
			log.Error("Issue in retrieving global svc", "Name=", globalSvcName)
			log.Error(err)
			log.Info("Skipping update for now.")
			return
		}

		globalSvcPort := globalSvc.Spec.Ports

		if !reflect.DeepEqual(targetSvcPort, globalSvcPort) {
			//Check port by port, to make sure there is no change.
			//Create map to do efficient checking
			log.Info("Looks like there is difference in Ports ", "TargetSvcPort= ", targetSvcPort, " GlobalSvcPort=", globalSvcPort, " syncing config.")
			mapPort := make(map[corev1.ServicePort]corev1.ServicePort)
			for _, port := range globalSvcPort {
				mapPort[port] = port
			}

			for _, port := range targetSvcPort {
				if _, ok := mapPort[port]; !ok {
					log.Info("Port= ", port, " doesn't exist in global. Adding.")
					//TO DO : Currently just adds new port, make sure that even remove unused ports.
					//There could be the case that service which is not being updated now, is using the same port.
					globalSvcPort = append(globalSvcPort, *port.DeepCopy())
				}
			}

			if !reflect.DeepEqual(globalSvcPort, globalSvc.Spec.Ports) {
				//Only update ports.
				defaultCreateOptions := metav1.CreateOptions{}

				for i, p := range globalSvcPort {
					//Create port name with index as API doenst allow to update port when there are multiple ones.
					p.Name = "port-" + fmt.Sprint(i)
					globalSvcPort[i] = p
				}

				//Update all the ports, cause its addition.
				globalSvc.Spec.Ports = globalSvcPort
				//Again make sure there is change in ports and its different from new service.
				log.Debug("Updated Global service  : ", "Ports to update=", globalSvcPort)
				log.Debug(globalSvc.Spec.Ports)
				_, err := client.CoreV1().Services(GLOBAL_SVC_NAMESPACE).Update(ctx.ctx, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
				if err != nil {
					log.Error("Unable to update ports, for global service ", "name=", globalSvcName)
					log.Error(err)
				}
			}
		}

		ctx.log.Info("New service resource version =  \n", newService.Name)

	}
}

func handleServiceDelete(ctx *Context, obj interface{}) {
	service, ok := obj.(*corev1.Service)
	if !ok {
		ctx.log.Errorln("Failed to cast object to Service type")
		return
	}
	log := ctx.log

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]
	_, err := ctx.client.CoreV1().Services("").List(ctx.ctx, metav1.ListOptions{LabelSelector: targetClusterame})
	if err != nil {
		log.Error("Unable to list services  for label: ", targetClusterame)
		log.Error(err)
		return
	}

	//TO DO : Make sure if all the services are deleted for cluster, global service is also deleted.
	log.Info("Service being Deleted,  ", service.Name)
}

func getSharedInformer(ctx Context, svcFilter labels.Selector) cache.SharedInformer {
	//Get enformer and add event handler
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return ctx.client.CoreV1().Services("").List(context.Background(), metav1.ListOptions{LabelSelector: svcFilter.String()})
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return ctx.client.CoreV1().Services("").Watch(context.Background(), metav1.ListOptions{LabelSelector: svcFilter.String()})
			},
		},
		&corev1.Service{},
		time.Second*90, // sync after 90 seconds
		cache.Indexers{},
	)

	// Register the event handlers for additions and updates
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			handleServiceAdd(&ctx, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			handleServiceUpdate(&ctx, oldObj, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			handleServiceDelete(&ctx, obj)
		},
	})

	return informer
}

type Context struct {
	ctx    context.Context
	log    logrus.Logger
	client kubernetes.Clientset
}

type globalServiceMirrorInformers struct {
	//Service handle rinformer
	svcInformer cache.SharedInformer
	//Endpoint handler informer
	// epInformer cache.ResourceEventHandler
}

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

	log.Infof("Starting Global Mirror")

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	}

	flag.Parse()

	// use the current context in kubeconfig
	config, err := client.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Panicf("Probably running Inside Cluster :%s", err.Error())
	}

	// creates the clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Panicf("Issue in building client from config : %s", err.Error())
	}

	//Create context
	ctx := &Context{
		ctx:    context.TODO(),
		log:    *log,
		client: *client,
	}

	//Make sure it runs in loop.
	stopCh := make(chan struct{})
	defer close(stopCh)

	svcFilter := getServiceFilter(*ctx)

	//Get informer in run in go routine.
	svcInformer := getSharedInformer(*ctx, svcFilter)

	//Build global informer
	globaMirrorInformer := globalServiceMirrorInformers{
		svcInformer: svcInformer,
	}

	go globaMirrorInformer.svcInformer.Run(stopCh)

	// Wait until the informer is synced
	if !cache.WaitForCacheSync(stopCh, globaMirrorInformer.svcInformer.HasSynced) {
		log.Panicln("Failed to sync informer cache")
	}

	if err != nil {
		log.Errorf("Unable to get running services for filter : %s, Error: %s", svcFilter.String(), err.Error())
	}

	// Run the program indefinitely
	<-stopCh
}
