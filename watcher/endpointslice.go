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
func (epsW *Watcher) handleEpsAdd(endpointslice discoveryv1.EndpointSlice) {

	epsW.log.Debugf("EndpointSlice has been appeared : %v", endpointslice.Name)
	// TO DO : Make sure it checks if global svc exists or not for this endpoint
	epsW.log.Debug("Global Service Exists for this Endpoint.")

	targetClusterName := endpointslice.GetLabels()["mirror.linkerd.io/cluster-name"]
	targetSvcName := endpointslice.GetLabels()["kubernetes.io/service-name"]

	//Check if EndpointSlice exists or not. x-targetClusterY-global
	targetEpsName := fmt.Sprintf("%v-global", targetSvcName)

	epsW.log.Debugf("Checking if EndpointSlice exist with Name : %v", targetEpsName)
	_, err := epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Get(epsW.Context, targetEpsName, metav1.GetOptions{})

	//Remove cluster name from the endpointslice to check if global service respective to it exists.
	// Target svc name will be : x-clusterName, so global service will be x-global
	globalSvcName := strings.Split(targetSvcName, fmt.Sprintf("-%v", targetClusterName))[0]
	globalSvcName = fmt.Sprintf("%v-global", globalSvcName)
	// TO DO : Check if the global service exists or not

	//If there is some other error that already exist. Log and return
	if err != nil && !apiError.IsAlreadyExists(err) {
		epsW.log.Infof("Creating EndpointSlice  : %v, w.r.t Global Service : %v", targetEpsName, globalSvcName)

		epsMeta := metav1.ObjectMeta{
			Name:      targetEpsName,
			Namespace: epsW.namespace,
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
			// Linkerd takes time after updating port in target cluster, in this time target svc might receive gateway ip.
			// Handle this condition
			if ep.Hostname == nil {
				epsW.log.Errorf("Skipping the update as Hostname is not present, mirror service is likley to have gone in weird state")
				epsW.log.Errorf("Check Endpointslice: %v look it has gateway ip, which it shouldn't have", endpointslice.Name)
				return
			}
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

		geps, err := epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Create(epsW.Context, &globalEndpointSlice, metav1.CreateOptions{})
		if err != nil {
			epsW.log.Errorf("Issue creating in EndpointSlice Name : %v", targetEpsName)
			epsW.log.Error(err)
			return
		}

		epsW.log.Infof("New Global EndpointSlice created : %v in Namespace : %v", geps.Name, geps.Namespace)
	}
}

// Handle endpointslice updates
func (epsW *Watcher) handleEpsUpdate(oldEndpoint, newEndpoint discoveryv1.EndpointSlice) {
	// Check if there is change in Endpoints and go ahead update the global endpointslice without even
	// comparing it as its supposed to be exact copy.
	if !reflect.DeepEqual(oldEndpoint.Endpoints, newEndpoint.Endpoints) {
		//Build global service name
		epsW.log.Debugf("Handling update for the Endpointslice: %v", newEndpoint.Name)
		targetSvcName := newEndpoint.GetLabels()["kubernetes.io/service-name"]

		//Check if EndpointSlice exists or not. x-targetClusterY-global
		globalEpsName := fmt.Sprintf("%v-global", targetSvcName)

		globalEndpointSlice, err := epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Get(epsW.Context, globalEpsName, metav1.GetOptions{})

		if err != nil {
			epsW.log.Errorf("Unable to get Global EndpointSlice before reflecting update: %v", err)
			return
		}

		// Get the addresses, modify hostname add target clustername at the end
		// And instead of comparing it rewrite/update the respective endpointslice as it supposed to be replica.
		newEpAddresses := make([]discoveryv1.Endpoint, 0)
		targetEndpoints := newEndpoint.DeepCopy().Endpoints
		targetClusterName := newEndpoint.GetLabels()["mirror.linkerd.io/cluster-name"]
		for _, addr := range targetEndpoints {
			// Linkerd takes time after updating port in target cluster, in this time target svc might receive in gateway ip.
			// Handle this condition
			if addr.Hostname == nil {
				epsW.log.Errorf("Skipping the update as Hostname is not present, mirror service is likley to have gone in weird state")
				epsW.log.Errorf("Check Endpointslice: %v. Probably it has gateway ip, which it shouldn't have", newEndpoint.Name)
				epsW.log.Errorf("It might recover automatically")
				return
			}
			hostname := fmt.Sprintf("%v-%v", *addr.Hostname, targetClusterName)
			addr.Hostname = &hostname
			newEpAddresses = append(newEpAddresses, addr)
		}

		// epsW.log.Debugf("Updating endpoints with new addresses : %v", newEpAddresses)
		globalEndpointSlice.Endpoints = newEpAddresses
		globalEndpointSlice.Ports = newEndpoint.DeepCopy().Ports

		epsW.log.Infof("Updating the endpointslice: %v", globalEndpointSlice.Name)
		// Update the endpoint slice
		globalEndpointSlice, err = epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Update(epsW.Context, globalEndpointSlice.DeepCopy(), metav1.UpdateOptions{})
		if err != nil {
			epsW.log.Errorf("Unable to update the Global Endpoint Slice: %v for update of EndpointSlice: %v, of target cluster: %v", globalEndpointSlice.Name, newEndpoint.Name, targetClusterName)
			epsW.log.Error(err)
			return
		}
	}

	epsW.log.Debugf("Endpointslice has been updated: %v", newEndpoint.Name)

}

// Handle endpoitslice delete, delete respective global endpointslice.
func (epsW *Watcher) handleEpsDelete(endpointslice discoveryv1.EndpointSlice) {

	epsW.log.Infof("Got the the delete for Endpointslice: %v", endpointslice.Name)

	targetSvcName := endpointslice.GetLabels()["kubernetes.io/service-name"]

	globalEpsName := fmt.Sprintf("%v-global", targetSvcName)

	globalEp, err := epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Get(epsW.Context, globalEpsName, metav1.GetOptions{})
	if err != nil {
		epsW.log.Errorf("Unable to get globalendpointslice: %v", globalEpsName)
		return
	}

	//Delete respective global endpointslice
	err = epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).Delete(epsW.Context, globalEpsName, metav1.DeleteOptions{})
	if err != nil {
		epsW.log.Errorf("Unable to delete globalendpointslice: %v, respective to: %v", globalEp.Name, endpointslice.Name)
		return
	}

	epsW.log.Infof("Global Endpointslice deleted: %v, respective to: %v", globalEp.Name, endpointslice.Name)

	// DELETE RESPECTIVE GLOBAL SERVICE IF THERE ARE NO MORE ENDPOINTSLICES.....

	//Get global service name
	globalSvcName := globalEp.GetLabels()["kubernetes.io/service-name"]
	epsW.log.Debugf("If there are no more endpointslices exist for Global Service: %v else global service will be deleted", globalSvcName)

	//Find if there endpointslices exists for this global service if not remove its
	epsList, err := epsW.clientset.DiscoveryV1().EndpointSlices(epsW.namespace).List(epsW.Context, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("kubernetes.io/service-name=%v", globalSvcName),
	})

	if err != nil {
		epsW.log.Errorf("Unable to get endpoinslices wrt to global service: %v, err: %v", globalSvcName, err)
		return
	}

	if len(epsList.Items) > 0 {
		epsW.log.Debugf("Passing deletion of respetive global service as other endpointslices exist: %v", globalSvcName)
		return
	}

	//It means no endpointslices exists for respective global service so it can be deleted.
	err = epsW.clientset.CoreV1().Services(epsW.namespace).Delete(epsW.Context, globalSvcName, metav1.DeleteOptions{})
	if err != nil {
		epsW.log.Errorf("Issue deleting globals service name: %v", globalSvcName)
		epsW.log.Error(err)
		return
	}

	epsW.log.Infof("Global service: %v is also deleted as there are no more endpoinslices attached to it.", globalSvcName)
}
