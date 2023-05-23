package watcher

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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Endpointslice watcher to watch endpointslice with target filter
type EndpointSliceWatcher struct {
	context.Context
	clientset kubernetes.Clientset
	log       *logrus.Entry
	Filter    labels.Selector
	Informer  cache.SharedInformer
	// Name space to run watcher & mirror endpointslices.
	namespace string
}

// Build new EndpointSlices watcher
func NewEndpointSlicesWatcher(ctx context.Context, client *kubernetes.Clientset, log *logrus.Logger, ns *string) EndpointSliceWatcher {
	eps := &EndpointSliceWatcher{
		Context:   ctx,
		clientset: *client,
		// Distinguish between different loggers
		log: log.WithField("[logger]", "endpointslice"),
	}
	eps.Filter = eps.createEpsFilter()
	eps.Informer = eps.createSharedInformer()
	eps.namespace = *ns
	return *eps
}

func (eps *EndpointSliceWatcher) createEpsFilter() labels.Selector {
	/* Get running Endpointslices in all the names, labelled with "mirror.linkerd.io/mirrored-service: true"
	 */
	mirrorEpsLabel, err := labels.NewRequirement("mirror.linkerd.io/mirrored-service", selection.Equals, []string{"true"})
	if err != nil {
		eps.log.Errorf("Unable to generate error requirement, Err : %v", err.Error())
	}

	// Create label requirements for "mirror.linkerd.io/headless-mirror-svc-name" not existing
	mirrorEpsParentLabel, err := labels.NewRequirement("mirror.linkerd.io/headless-mirror-svc-name", selection.DoesNotExist, []string{})
	if err != nil {
		eps.log.Errorf("Unable to generate error requirement, Err : %v", err.Error())
	}

	// Create the label selector
	epsFilter := labels.NewSelector()
	epsFilter = epsFilter.Add(*mirrorEpsLabel)
	epsFilter = epsFilter.Add(*mirrorEpsParentLabel)

	return epsFilter
}

func (eps *EndpointSliceWatcher) createSharedInformer() cache.SharedInformer {
	epsFilter := eps.Filter
	//Get enformer and add event handler
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return eps.clientset.DiscoveryV1().EndpointSlices("").List(context.Background(), metav1.ListOptions{LabelSelector: epsFilter.String()})
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return eps.clientset.DiscoveryV1().EndpointSlices("").Watch(context.Background(), metav1.ListOptions{LabelSelector: epsFilter.String()})
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

	eps.log.Infof("EndpointSlice has been appeared : %v", endpointslice.Name)
	// TO DO : Make sure it checks if global svc exists or not for this endpoint
	eps.log.Debug("Global Service Exists for this Endpoint.")

	targetClusterName := endpointslice.GetLabels()["mirror.linkerd.io/cluster-name"]
	targetSvcName := endpointslice.GetLabels()["kubernetes.io/service-name"]

	//Check if EndpointSlice exists or not. x-targetClusterY-global
	targetEpsName := fmt.Sprintf("%v-global", targetSvcName)

	eps.log.Debugf("Checking if EndpointSlice exist with Name : %v", targetEpsName)
	_, err := eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Get(eps.Context, targetEpsName, metav1.GetOptions{})

	//Remove cluster name from the endpointslice to check if global service respective to it exists.
	// Target svc name will be : x-clusterName, so global service will be x-global
	globalSvcName := strings.Split(targetSvcName, fmt.Sprintf("-%v", targetClusterName))[0]
	globalSvcName = fmt.Sprintf("%v-global", globalSvcName)
	// TO DO : Check if the global service exists or not

	//If there is some other error that already exist. Log and return
	if err != nil && !apiError.IsAlreadyExists(err) {
		eps.log.Infof("Creating EndpointSlice  : %v, w.r.t Global Service : %v", targetEpsName, globalSvcName)

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

		geps, err := eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Create(eps.Context, &globalEndpointSlice, metav1.CreateOptions{})
		if err != nil {
			eps.log.Errorf("Issue creating in EndpointSlice Name : %v", targetEpsName)
			eps.log.Error(err)
			return
		}

		eps.log.Infof("New Global EndpointSlice created : %v in Namespace : %v", geps.Name, geps.Namespace)
	}
	eps.log.Infof("Endpointslice has been Handled : %v", endpointslice.Name)
}

// Handle endpointslice updates
func (eps *EndpointSliceWatcher) handleServiceUpdate(oldObj, newObj interface{}) {
	// Check if there is difference betweend endpoints and ports. If there is change
	// Work on respective endpoint and port.
	// If nothing has changed, return. https://github.com/kubernetes/client-go/issues/529
	if reflect.DeepEqual(oldObj, newObj) {
		eps.log.Debugf("Nothing has changed in object, skipping update.")
		return
	}

	oldEndpoint := oldObj.(*discoveryv1.EndpointSlice)
	newEndpoint := newObj.(*discoveryv1.EndpointSlice)

	// Check if there is change in Endpoints and go ahead update the global endpointslice without even
	// comparing it as its supposed to be exact copy.
	if !reflect.DeepEqual(oldEndpoint.Endpoints, newEndpoint.Endpoints) {
		//Build global service name
		eps.log.Debugf("Handling update for the Endpointslice: %v", newEndpoint.Name)
		targetSvcName := newEndpoint.GetLabels()["kubernetes.io/service-name"]

		//Check if EndpointSlice exists or not. x-targetClusterY-global
		globalEpsName := fmt.Sprintf("%v-global", targetSvcName)

		globalEndpointSlice, err := eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Get(eps.Context, globalEpsName, metav1.GetOptions{})

		if err != nil {
			eps.log.Errorf("Unable to get Global EndpointSlice before reflecting update: %v", err)
			return
		}

		// Get the addresses, modify hostname add target clustername at the end
		// And instead of comparing it rewrite/update the respective endpointslice as it supposed to be replica.
		newEpAddresses := make([]discoveryv1.Endpoint, 0)
		targetEndpoints := newEndpoint.Endpoints
		targetClusterName := newEndpoint.GetLabels()["mirror.linkerd.io/cluster-name"]
		for _, addr := range targetEndpoints {
			hostname := fmt.Sprintf("%v-%v", *addr.Hostname, targetClusterName)
			addr.Hostname = &hostname
			newEpAddresses = append(newEpAddresses, addr)
		}

		eps.log.Debugf("Updating endpoints with new addresses : %v", newEpAddresses)
		globalEndpointSlice.Endpoints = newEpAddresses
		globalEndpointSlice.Ports = newEndpoint.DeepCopy().Ports

		eps.log.Infof("Updating the endpointslice: %v", globalEndpointSlice.Name)
		// Update the endpoint slice
		globalEndpointSlice, err = eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Update(eps.Context, globalEndpointSlice.DeepCopy(), metav1.UpdateOptions{})
		if err != nil {
			eps.log.Errorf("Unable to update the Global Endpoint Slice: %v for update of EndpointSlice: %v, of target cluster: %v", globalEndpointSlice.Name, newEndpoint.Name, targetClusterName)
			eps.log.Error(err)
			return
		}
	}

	eps.log.Debugf("Endpointslice has been updated: %v", newEndpoint.Name)

}

// Handle endpoitslice delete
func (eps *EndpointSliceWatcher) handleServiceDelete(oldObj interface{}) {
	eps.log.Info("Got the the delete for Endpointslice")
}
