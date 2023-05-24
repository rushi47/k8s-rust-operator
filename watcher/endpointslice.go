package watcher

import (
	"fmt"
	"reflect"
	"strings"

	discoveryv1 "k8s.io/api/discovery/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Handle add events
func (eps *Watcher) handleEpsAdd(endpointslice discoveryv1.EndpointSlice) {

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
				"mirror.linkerd.io/global-mirror":          "true",
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
}

// Handle endpointslice updates
func (eps *Watcher) handleEpsUpdate(oldEndpoint, newEndpoint discoveryv1.EndpointSlice) {
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
		targetEndpoints := newEndpoint.DeepCopy().Endpoints
		targetClusterName := newEndpoint.GetLabels()["mirror.linkerd.io/cluster-name"]
		for _, addr := range targetEndpoints {
			hostname := fmt.Sprintf("%v-%v", *addr.Hostname, targetClusterName)
			addr.Hostname = &hostname
			newEpAddresses = append(newEpAddresses, addr)
		}

		// eps.log.Debugf("Updating endpoints with new addresses : %v", newEpAddresses)
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

// Handle endpoitslice delete, delete respective global endpointslice.
func (eps *Watcher) handleEpsDelete(endpointslice discoveryv1.EndpointSlice) {

	eps.log.Info("Got the the delete for Endpointslice: %v", endpointslice.Name)

	targetSvcName := endpointslice.GetLabels()["kubernetes.io/service-name"]

	globalEpsName := fmt.Sprintf("%v-global", targetSvcName)

	globalEp, err := eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Get(eps.Context, globalEpsName, metav1.GetOptions{})
	if err != nil {
		eps.log.Errorf("Unable to get globalendpointslice: %v", globalEpsName)
		return
	}

	//Delete respective global endpointslice
	err = eps.clientset.DiscoveryV1().EndpointSlices(eps.namespace).Delete(eps.Context, globalEpsName, metav1.DeleteOptions{})
	if err != nil {
		eps.log.Errorf("Unable to delete globalendpointslice: %v, respective to: %v", globalEp.Name, endpointslice.Name)
		return
	}

	eps.log.Infof("Global Endpointslice deleted: %v, respective to: %v", globalEp.Name, endpointslice.Name)
}
