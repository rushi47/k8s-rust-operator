// Package defines various watcher like Service and EndpointSlice
package watcher

import (
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (svcW *Watcher) checkNameSpaceExists() {

	// Check if the namespace already exists
	namespace := svcW.namespace
	_, err := svcW.clientset.CoreV1().Namespaces().Get(svcW.Context, namespace, metav1.GetOptions{})
	if err != nil {
		if apiError.IsAlreadyExists(err) {
			svcW.log.Debugf("Skipped creating namespace '%v'; already exists", namespace)
			return
		}
		svcW.log.Errorf("Failed to get namespace '%s': %v", err)
		return
	}
	// Create the namespace
	newNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	_, err = svcW.clientset.CoreV1().Namespaces().Create(svcW.Context, newNamespace, metav1.CreateOptions{})
	if err != nil {
		svcW.log.Errorf("Issue creating namespace: %v", err)
		return
	}

	svcW.log.Infof("Namespace '%s' created", namespace)
}

// Check if the existing service is in sync [JUST PORTS FOR NOW, IF CHANGE RETURN UPDATED SLICE]
func (svcW *Watcher) checkParityofService(targetSvc *corev1.Service, globalSvc *corev1.Service) ([]corev1.ServicePort, bool) {

	targetSvcPort := targetSvc.Spec.DeepCopy().Ports
	globalSvcPort := globalSvc.Spec.DeepCopy().Ports
	if !reflect.DeepEqual(targetSvcPort, globalSvcPort) {
		//Check port by port, to make sure there is no change.
		//Create map to do efficient checking
		/*
						GlobalService              MirrorService1               mirrorservice2
			            - port: 80                 - port: 80                    - port: 90
						  targetPort: 7000           targetPort: 6000              targetport: 8000
		*/
		mapPort := make(map[corev1.ServicePort]corev1.ServicePort)
		for _, port := range globalSvcPort {
			port.Name = ""
			mapPort[port] = corev1.ServicePort{}
			mapPort[port] = port
		}
		// log.Debugf("Value of global svc ports after removing name: %v", mapPort)
		for _, port := range targetSvcPort {
			if _, ok := mapPort[port]; !ok {
				svcW.log.Infof("Port= %v doesn't exist in global. Adding.", port)
				globalSvcPort = append(globalSvcPort, *port.DeepCopy())
			}
		}

		//Again make sure there is change in ports if there is change, update the ports.
		//Checking now it should return true.
		if !reflect.DeepEqual(globalSvcPort, globalSvc.Spec.Ports) {
			//Only update ports.
			svcW.log.Debugf("Looks like there is difference in Ports NewPorts=%v, ExistingPorts=%v  syncing config", globalSvcPort, globalSvc.Spec.Ports)

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

func (svcW *Watcher) handleServiceAdd(service corev1.Service) {
	/*
		- Check if Global Service is added wrt current service.
		- Global service name of svc: x-target will be x-global.

	*/

	svcW.log.Debugf("New Target Service Added, svcName= %v", service.Name)

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]

	targetSvc := service.DeepCopy()

	// Check if global/aggregator service exists.
	// We dont have this object in Local Cache so check directly.
	// TO DO: Add global object in local cache.
	globalSvcName := strings.Split(targetSvc.Name, fmt.Sprintf("-%s", targetClusterame))[0]
	globalSvcName = globalSvcName + "-global"
	svcW.log.Debugf("Checking global Svc Named= %v  if exists", globalSvcName)
	globalSvc, err := svcW.clientset.CoreV1().Services(svcW.namespace).Get(svcW.Context, globalSvcName, metav1.GetOptions{})

	//If global service doesnt exist cerate it
	if err != nil && !apiError.IsAlreadyExists(err) {

		/*
			- Spin up the new global service with cardinal index as x-global,
			Which will be aggregator for mirrored services from targetSvc. cluster x-targetSvc.0, x-targetSvc.1
			Global service will always be created in Default.
		*/
		svcW.checkNameSpaceExists()
		svcW.log.Infof("New Global Service will be created Name=%v in Namespace=%v", globalSvcName, svcW.namespace)

		svcMeta := &metav1.ObjectMeta{
			Name:      globalSvcName,
			Namespace: svcW.namespace,
			Labels: map[string]string{
				"mirror.linkerd.io/global-mirror": "true",
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
		_, err := svcW.clientset.CoreV1().Services(svcW.namespace).Create(svcW.Context, globalService, defaultCreateOptions)
		if !apiError.IsAlreadyExists(err) && err != nil {
			svcW.log.Errorf("Issue with service creation, Name=%v", globalSvcName)
			svcW.log.Error(err)
			return
		}

	} else {
		// If service already exists, check if the port from the target service are inside global svcW.
		svcW.log.Debugf("Skipping Creation of New Global Service, Named=%v already exists", globalSvcName)
		svcW.log.Debugf("Checking if the Spec is synced. [Currently only checks for ports.]")
		globalSvcPort, diff := svcW.checkParityofService(targetSvc, globalSvc)
		if diff {
			// Update all the ports, cause its addition.
			globalSvc.Spec.Ports = globalSvcPort
			defaultCreateOptions := metav1.CreateOptions{}
			_, err := svcW.clientset.CoreV1().Services(svcW.namespace).Update(svcW.Context, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
			if err != nil {
				svcW.log.Errorf("Unable to update ports, for global service Name=%v", globalSvcName)
				svcW.log.Error(err)
				return
			}
			svcW.log.Infof("Updated global service port: %v", globalSvc.Name)
		}

	}

}

// Function to handle service updates
func (svcW *Watcher) handleServiceUpdate(oldService corev1.Service, newService corev1.Service) {

	svcW.log.Infof("Handling update for Service: %v ", oldService.Name)

	//Get target clusters.
	targetClusterame := newService.GetLabels()["mirror.linkerd.io/cluster-name"]

	if !reflect.DeepEqual(oldService.Spec.Ports, newService.Spec.Ports) {

		// Assuming global/aggregator service exists.
		// TO DO: Also add logic to handle
		globalSvcName := strings.Split(newService.Name, fmt.Sprintf("-%s", targetClusterame))[0]
		globalSvcName = globalSvcName + "-global"

		svcW.log.Debugf("Checking global Svc Named=%v if exists", globalSvcName)

		globalSvc, err := svcW.clientset.CoreV1().Services("default").Get(svcW.Context, globalSvcName, metav1.GetOptions{})

		if !apiError.IsAlreadyExists(err) && err != nil {
			svcW.log.Errorf("Issue in retrieving global svc Name=%v", globalSvcName)
			svcW.log.Error(err)
			svcW.log.Infof("Skipping update for now.")
			return
		}

		globalSvcPort, diff := svcW.checkParityofService(&newService, globalSvc)
		if diff {
			//Update all the ports, cause its addition.
			globalSvc.Spec.Ports = globalSvcPort
			defaultCreateOptions := metav1.CreateOptions{}
			//Again make sure there is change in ports and its different from new service.
			svcW.log.Debugf("Updating Global service, Ports to update=%v, existing ports=%v", globalSvcPort, globalSvc.Spec.Ports)
			_, err := svcW.clientset.CoreV1().Services(svcW.namespace).Update(svcW.Context, globalSvc, metav1.UpdateOptions(defaultCreateOptions))
			if err != nil {
				svcW.log.Errorf("Unable to update ports, for global service, Name=%v", globalSvcName)
				svcW.log.Error(err)
			}
		}

	}
	svcW.log.Infof("Handled update for service: %v", newService.Name)
}

func (svcW *Watcher) handleServiceDelete(service corev1.Service) {

	targetClusterame := service.GetLabels()["mirror.linkerd.io/cluster-name"]
	_, err := svcW.clientset.CoreV1().Services("").List(svcW.Context, metav1.ListOptions{LabelSelector: targetClusterame})
	if err != nil {
		svcW.log.Errorf("Unable to list services  for label: %v", targetClusterame)
		svcW.log.Error(err)
		return
	}

	//TO DO : Make sure if all the services are deleted for cluster, global service is also deleted.
	svcW.log.Infof("Service being Deleted Name=%v ", service.Name)
}
