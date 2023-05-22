package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

type EndpointSliceWatcher struct {
	GlobalWatcher
	log *logrus.Entry
}

// Build new EndpointSlices watcher
func NewEndpointSlicesWatcher(ctx Context, ns *string) EndpointSliceWatcher {
	eps := &EndpointSliceWatcher{
		GlobalWatcher: GlobalWatcher{
			ctx: ctx,
		},
	}
	eps.Filter = eps.createEpsFilter()
	eps.informer = eps.createSharedInformer()
	eps.namespace = *ns
	//Clear distinguish between log
	eps.log = eps.ctx.log.WithField("[logger]", "endpointslice")
	return *eps
}

func (eps *EndpointSliceWatcher) createEpsFilter() labels.Selector {
	/* Get running Endpointslices in all the names, labelled with "mirror.linkerd.io/mirrored-service: true"
	 */
	ctx := eps.ctx
	mirrorEpsLabel, err := labels.NewRequirement("mirror.linkerd.io/mirrored-service", selection.Equals, []string{"true"})
	if err != nil {
		ctx.log.Errorf("Unable to generate error requirement, Err : %v", err.Error())
	}

	// Create label requirements for "mirror.linkerd.io/headless-mirror-svc-name" not existing
	mirrorEpsParentLabel, err := labels.NewRequirement("mirror.linkerd.io/headless-mirror-svc-name", selection.DoesNotExist, []string{})
	if err != nil {
		ctx.log.Errorf("Unable to generate error requirement, Err : %v", err.Error())
	}

	// Create the label selector
	epsFilter := labels.NewSelector()
	epsFilter = epsFilter.Add(*mirrorEpsLabel)
	epsFilter = epsFilter.Add(*mirrorEpsParentLabel)

	return epsFilter
}

func (eps *EndpointSliceWatcher) createSharedInformer() cache.SharedInformer {
	ctx := eps.ctx
	epsFilter := eps.Filter
	//Get enformer and add event handler
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return ctx.client.DiscoveryV1().EndpointSlices("").List(context.Background(), metav1.ListOptions{LabelSelector: epsFilter.String()})
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return ctx.client.DiscoveryV1().EndpointSlices("").Watch(context.Background(), metav1.ListOptions{LabelSelector: epsFilter.String()})
			},
		},
		&discoveryv1.EndpointSlice{},
		time.Second*90, // sync after 90 seconds
		cache.Indexers{},
	)

	// Register the event handlers for additions and updates
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    eps.handleServiceAdd,
		UpdateFunc: eps.handleServiceUpdate,
		DeleteFunc: eps.handleServiceDelete,
	})

	return informer
}

/*----------------- EVENT HANDLERS FOR ENDPONTSLICES ----------------------------*/

// Handle add events
func (eps *EndpointSliceWatcher) handleServiceAdd(obj interface{}) {
	endpointslice, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		eps.log.Errorf("Issue in casting object to Endpointslice : %v", ok)
		return
	}

	log := eps.log
	client := eps.ctx.client

	log.Infof("EndpointSlice has been appeared : %v", endpointslice.Name)
	// TO DO : Make sure it checks if global svc exists or not for this endpoint
	log.Debug("Global Service Exists for this Endpoint.")

	targetClusterName := endpointslice.GetLabels()["mirror.linkerd.io/cluster-name"]
	targetSvcName := endpointslice.GetLabels()["kubernetes.io/service-name"]

	//Check if EndpointSlice exists or not. x-targetClusterY-global
	targetEpsName := fmt.Sprintf("%v-global", targetSvcName)

	log.Debug("Checking if EndpointSlice exist with Name : %v", targetEpsName)
	_, err := client.DiscoveryV1().EndpointSlices(eps.namespace).Get(eps.ctx.ctx, targetEpsName, metav1.GetOptions{})

	//Remove cluster name from the endpointslice to check if global service respective to it exists.
	// Target svc name will be : x-clusterName, so global service will be x-global
	globalSvcName := strings.Split(targetSvcName, fmt.Sprintf("-%v", targetClusterName))[0]
	globalSvcName = fmt.Sprintf("%v-global", globalSvcName)
	// TO DO : Check if the global service exists or not

	//If there is some other error that already exist. Log and return
	if err != nil && !apiError.IsAlreadyExists(err) {
		log.Infof("Creating EndpointSlice  : %v, w.r.t Global Service : %v", targetEpsName, globalSvcName)

		epsMeta := metav1.ObjectMeta{
			Name:      targetEpsName,
			Namespace: eps.namespace,
			Labels: map[string]string{
				"kubernetes.io/service-name":               globalSvcName,
				"mirror.linkerd.io/target-mirror-svc-name": targetSvcName,
				"mirror.linkerd.io/cluster-name":           targetClusterName,
			},
		}

		/*
			- Get the Endpoints from target endpointslice
			- Make sure that the each hostname will have cluster i.e if hostname in target endpointslice is
			x-set-1, in the slice we are creating make sure it is : x-set-1-targetclustername
			- So that we get A records as we required.
		*/

		targetEndpoints := endpointslice.Endpoints
		endpointSliceGlobal := make([]discoveryv1.Endpoint, 0)
		for _, ep := range targetEndpoints {
			//Add clustername to the hostname
			hostname := fmt.Sprintf("%v-%v", *ep.Hostname, targetClusterName)
			ep.Hostname = &hostname
			endpointSliceGlobal = append(endpointSliceGlobal, ep)
		}

		globalEndpointSlice := discoveryv1.EndpointSlice{
			Endpoints:   endpointSliceGlobal,
			Ports:       endpointslice.DeepCopy().Ports,
			AddressType: endpointslice.DeepCopy().AddressType,
			ObjectMeta:  epsMeta,
		}

		geps, err := client.DiscoveryV1().EndpointSlices(eps.namespace).Create(eps.ctx.ctx, &globalEndpointSlice, metav1.CreateOptions{})
		if err != nil {
			log.Errorf("Issue creating in EndpointSlice Name : %v", targetEpsName)
			log.Error(err)
			return
		}

		log.Info("New Global EndpointSlice created : %v in Namespace : %v", geps.Name, geps.Namespace)
	}
	log.Info("Endpointslice has been Handled : %v", endpointslice.Name)
}

// Handle endpointslice updates
func (svc *EndpointSliceWatcher) handleServiceUpdate(oldObj, newObj interface{}) {
	// Check if there is difference betweend endpoints and ports. If there is change
	// Work on respective endpoint and port.
	//If nothing has changed, return. https://github.com/kubernetes/client-go/issues/529
	if !reflect.DeepEqual(oldObj, newObj) {
		return
	}

	oldEndpoint := oldObj.(*discoveryv1.EndpointSlice)
	newEndpoint := newObj.(*discoveryv1.EndpointSlice)

	// Check if there is change in Endpoints
	if !reflect.DeepEqual(oldEndpoint.Endpoints, newEndpoint.Endpoints) {
		// Get the addresses, without hostname and the compare.
		newEpAddresses := make([]discoveryv1.Endpoint, 4)
		for _, addr := range newEndpoint.Endpoints {
			newEpAddresses = append(newEpAddresses)
		}

	}

}

// Handle endpoitslice delete
func (svc *EndpointSliceWatcher) handleServiceDelete(oldObj interface{}) {
	svc.log.Info("Got the the delete for Endpointslice")
}
