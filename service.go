package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// Struct to build service watcher which looks after target services.
type ServiceWatcher struct {
	ctx       Context
	svcFilter labels.Selector
	informer  cache.SharedInformer
	// Name space to run watcher & mirror services.
	namespace string
}

// Build new service watcher
func NewServiceWatcher(ctx Context, ns *string) ServiceWatcher {
	svc := &ServiceWatcher{
		ctx: ctx,
	}
	svc.svcFilter = svc.createServiceFilter()
	svc.informer = svc.createSharedInformer()
	svc.namespace = *ns
	return *svc
}

func (svc *ServiceWatcher) checkNameSpaceExists() {

	// Check if the namespace already exists
	client := svc.ctx.client
	log := svc.ctx.log
	namespace := svc.namespace
	_, err := client.CoreV1().Namespaces().Get(svc.ctx.ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if apiError.IsAlreadyExists(err) {
			log.Debugf("Skipped creating namespace '%s'; already exists", namespace)
			return
		}
		log.Errorf("Failed to get namespace '%s': %v", err)
		return
	}
	// Create the namespace
	newNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	_, err = client.CoreV1().Namespaces().Create(svc.ctx.ctx, newNamespace, metav1.CreateOptions{})
	if err != nil {
		log.Errorf("Issue creating namespace: %v", err)
	}

	log.Infof("Namespace '%s' created", namespace)
}

func (svc *ServiceWatcher) createServiceFilter() labels.Selector {
	/* Get running services in all the names, labelled with "mirror.linkerd.io/mirrored-service: true"
	and which service which does have label : mirror.linkerd.io/headless-mirror-svc-name. Build selector for it.
	*/
	ctx := svc.ctx
	mirrorSvcLabel, err := labels.NewRequirement("mirror.linkerd.io/mirrored-service", selection.Equals, []string{"true"})
	if err != nil {
		ctx.log.Errorf("Unable to generate error requirement, Err: %v", err)
	}

	// Create label requirements for "mirror.linkerd.io/headless-mirror-svc-name" not existing
	mirrorSvcParentLabel, err := labels.NewRequirement("mirror.linkerd.io/headless-mirror-svc-name", selection.DoesNotExist, []string{})
	if err != nil {
		ctx.log.Errorf("Unable to generate error requirement, Err: %v", err)
	}

	// Create the label selector
	svcFilter := labels.NewSelector()
	svcFilter = svcFilter.Add(*mirrorSvcParentLabel)
	svcFilter = svcFilter.Add(*mirrorSvcLabel)

	return svcFilter
}

func (svc *ServiceWatcher) createSharedInformer() cache.SharedInformer {
	ctx := svc.ctx
	svcFilter := svc.svcFilter
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
		AddFunc:    svc.handleServiceAdd,
		UpdateFunc: svc.handleServiceUpdate,
		DeleteFunc: svc.handleServiceDelete,
	})

	return informer
}

// Check if the existing service is in sync [JUST PORTS FOR NOW, IF CHANGE RETURN UPDATED SLICE]
func (svc *ServiceWatcher) checkParityofService(targetSvc *corev1.Service, globalSvc *corev1.Service) ([]corev1.ServicePort, bool) {

	targetSvcPort := targetSvc.Spec.Ports
	globalSvcPort := globalSvc.Spec.Ports
	log := svc.ctx.log
	if !reflect.DeepEqual(targetSvcPort, globalSvcPort) {
		//Check port by port, to make sure there is no change.
		//Create map to do efficient checking
		mapPort := make(map[corev1.ServicePort]corev1.ServicePort)
		for _, port := range globalSvcPort {
			port.Name = ""
			mapPort[port] = corev1.ServicePort{}
			mapPort[port] = port
		}
		// log.Debugf("Value of global svc ports after removing name: %v", mapPort)
		for _, port := range targetSvcPort {
			if _, ok := mapPort[port]; !ok {
				log.Infof("Port= %v doesn't exist in global. Adding.", port)
				globalSvcPort = append(globalSvcPort, *port.DeepCopy())
			}
		}

		//Again make sure there is change in ports if there is change, update the ports.
		//Checking now it should return true.
		if !reflect.DeepEqual(globalSvcPort, globalSvc.Spec.Ports) {
			//Only update ports.
			log.Debugf("Looks like there is difference in Ports NewPorts=%v, ExistingPorts=%v  syncing config", globalSvcPort, globalSvc.Spec.Ports)

			for i, p := range globalSvcPort {
				//Create port name with index as API doenst allow to update port when there are multiple ones.
				p.Name = "port-" + fmt.Sprint(i)
				globalSvcPort[i] = p
			}

			return globalSvcPort, true
		}
		return make([]corev1.ServicePort, 0), false
	}
	return make([]corev1.ServicePort, 0), false
}

/* -------------------- EVENT HANDLERS FOR SERVICE ---------------------- */

// Function to handle service additions
func (svc *ServiceWatcher) handleServiceAdd(obj interface{}) {
	/*
		- Check if Global Service is added wrt current service.
		- Global service name of svc: x-target will be x-global.

	*/
	ctx := svc.ctx
	log := ctx.log
	client := ctx.client

	service, ok := obj.(*corev1.Service)
	log.Infof("Handling Add for Service: %v ", service.Name)
	if !ok {
		log.Errorf("Failed to cast object to Service type")
		return
	}
	log.Infof("New Target Service Added, svcName= %v", service.Name)

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]

	targetSvc := service.DeepCopy()

	// Check if global/aggregator service exists.
	// We dont have this object in Local Cache so check directly.
	// TO DO: Add global object in local cache.
	globalSvcName := strings.Split(targetSvc.Name, fmt.Sprintf("-%s", targetClusterame))[0]
	globalSvcName = globalSvcName + "-global"
	log.Infof("Checking global Svc Named= %v  if exists", globalSvcName)
	globalSvc, err := client.CoreV1().Services(svc.namespace).Get(context.Background(), globalSvcName, metav1.GetOptions{})

	//If global service doesnt exist cerate it
	if err != nil && !apiError.IsAlreadyExists(err) {

		/*
			- Spin up the new global service with cardinal index as x-global,
			Which will be aggregator for mirrored services from targetSvc. cluster x-targetSvc.0, x-targetSvc.1
			Global service will always be created in Default.
		*/
		svc.checkNameSpaceExists()
		log.Infof("New Global Service will be created Name=%v in Namespace=%v", globalSvcName, svc.namespace)

		svcMeta := &metav1.ObjectMeta{
			Name:      globalSvcName,
			Namespace: svc.namespace,
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
		_, err := client.CoreV1().Services(svc.namespace).Create(context.Background(), globalService, defaultCreateOptions)
		if !apiError.IsAlreadyExists(err) && err != nil {
			log.Errorf("Issue with service creation, Name=%v", globalSvcName)
			log.Error(err)
			return
		}

	} else {
		// If service already exists, check if the port from the target service are inside global svc.
		log.Debugf("Skipping Creation of New Global Service, Named=%v already exists", globalSvcName)
		log.Infof("Checking if the Spec is synced. [Currently only checks for ports.]")
		globalSvcPort, diff := svc.checkParityofService(targetSvc, globalSvc)
		if diff {
			// Update all the ports, cause its addition.
			globalSvc.Spec.Ports = globalSvcPort
			defaultCreateOptions := metav1.CreateOptions{}
			_, err := client.CoreV1().Services(svc.namespace).Update(ctx.ctx, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
			if err != nil {
				log.Errorf("Unable to update ports, for global service Name=%v", globalSvcName)
				log.Error(err)
				return
			}
		}

	}

	log.Infof("Global Service with Name=%v  should exist.", globalSvcName)
}

// Function to handle service updates
func (svc *ServiceWatcher) handleServiceUpdate(oldObj, newObj interface{}) {
	ctx := svc.ctx
	log := ctx.log

	//If nothing has changed, return. https://github.com/kubernetes/client-go/issues/529
	if reflect.DeepEqual(oldObj, newObj) {
		ctx.log.Debugf("Nothing has changed in object, skipping update.")
		return
	}

	oldService, ok := oldObj.(*corev1.Service)
	if !ok {
		ctx.log.Errorf("Failed to cast old object to Service type")
		return
	}
	newService, ok := newObj.(*corev1.Service)
	if !ok {
		ctx.log.Errorf("Failed to cast new object to Service type")
		return
	}

	log.Infof("Handling update for Service: %v ", oldService.Name)

	//Get target clusters.
	targetClusterame := newService.GetLabels()["mirror.linkerd.io/cluster-name"]

	if !reflect.DeepEqual(oldService.Spec.Ports, newService.Spec.Ports) {

		// Assuming global/aggregator service exists.
		// TO DO: Also add logic to handle
		log := ctx.log
		client := ctx.client
		globalSvcName := strings.Split(newService.Name, fmt.Sprintf("-%s", targetClusterame))[0]
		globalSvcName = globalSvcName + "-global"

		log.Debugf("Checking global Svc Named=%v if exists", globalSvcName)

		globalSvc, err := client.CoreV1().Services("default").Get(context.Background(), globalSvcName, metav1.GetOptions{})

		if !apiError.IsAlreadyExists(err) && err != nil {
			log.Errorf("Issue in retrieving global svc Name=%v", globalSvcName)
			log.Error(err)
			log.Infof("Skipping update for now.")
			return
		}

		globalSvcPort, diff := svc.checkParityofService(newService, globalSvc)
		if diff {
			//Update all the ports, cause its addition.
			globalSvc.Spec.Ports = globalSvcPort
			defaultCreateOptions := metav1.CreateOptions{}
			//Again make sure there is change in ports and its different from new service.
			log.Debugf("Updating Global service, Ports to update=%v, existing ports=%v", globalSvcPort, globalSvc.Spec.Ports)
			_, err := client.CoreV1().Services(svc.namespace).Update(ctx.ctx, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
			if err != nil {
				log.Errorf("Unable to update ports, for global service, Name=%v", globalSvcName)
				log.Error(err)
			}
		}

	}
	log.Infof("Handled update for service: %v", newService.Name)
}

func (svc *ServiceWatcher) handleServiceDelete(obj interface{}) {
	ctx := svc.ctx
	service, ok := obj.(*corev1.Service)
	log := ctx.log

	log.Infof("Handling Delete for Service: %v ", service.Name)

	if !ok {
		ctx.log.Errorf("Failed to cast object to Service type")
		return
	}

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]
	_, err := ctx.client.CoreV1().Services("").List(ctx.ctx, metav1.ListOptions{LabelSelector: targetClusterame})
	if err != nil {
		log.Errorf("Unable to list services  for label: %v", targetClusterame)
		log.Error(err)
		return
	}

	//TO DO : Make sure if all the services are deleted for cluster, global service is also deleted.
	log.Infof("Service being Deleted Name=%v ", service.Name)
}
