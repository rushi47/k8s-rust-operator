package watcher

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Watcher struct {
	InformersFactory informers.SharedInformerFactory
	log              *logrus.Logger
	clientset        kubernetes.Clientset
	namespace        string
	Context          context.Context
}

func NewWatch(ctx context.Context, client kubernetes.Clientset, log *logrus.Logger, namespace string) Watcher {
	watch := &Watcher{
		Context:          ctx,
		InformersFactory: informers.NewSharedInformerFactory(&client, time.Second*3),
		log:              log,
		clientset:        client,
		namespace:        namespace,
	}
	return *watch
}

func (w *Watcher) RegisterHandlers() {

	//Create informer for service
	svcInformer := w.InformersFactory.Core().V1().Services().Informer()
	svcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			service, ok := obj.(*corev1.Service)
			if !ok {
				w.log.Errorf("Failed to cast Service in Add")
				return
			}
			//If obj doesnt match the filter return
			if !w.Filter(service.ObjectMeta) {
				return
			}
			w.handleServiceAdd(*service.DeepCopy())
		},
		UpdateFunc: func(oldobj, obj interface{}) {
			newSvc, ok := obj.(*corev1.Service)
			if !ok {
				w.log.Errorf("Failed to cast Service in Update")
				return
			}
			oldSvc, ok := oldobj.(*corev1.Service)
			if !ok {
				w.log.Errorf("Failed to cast Service in Update")
				return
			}
			// If obj doesnt match the filter return or it doesnt match resource version return
			// https://github.com/kubernetes/client-go/issues/529
			if !w.Filter(newSvc.ObjectMeta) {
				return
			}
			if newSvc.ResourceVersion == oldSvc.ResourceVersion {
				return
			}

			w.handleServiceUpdate(*oldSvc.DeepCopy(), *newSvc.DeepCopy())
		},
		DeleteFunc: func(obj interface{}) {
			svc, ok := obj.(*corev1.Service)
			if !ok {
				w.log.Errorf("Failed to cast Service in Delete")
				return
			}
			// If obj doesnt match the filter return
			if !w.Filter(svc.ObjectMeta) {
				return
			}
			w.handleServiceDelete(*svc.DeepCopy())
		},
	})
	//Create Informer for endpointslice
	epsInformer := w.InformersFactory.Discovery().V1().EndpointSlices().Informer()
	epsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			eps, ok := obj.(*discoveryv1.EndpointSlice)
			if !ok {
				w.log.Errorf("Failed to cast Endpointslice")
				return
			}
			// If obj doesnt match the filter return
			if !w.Filter(eps.ObjectMeta) {
				return
			}
			w.handleEpsAdd(*eps.DeepCopy())
		},
		UpdateFunc: func(oldobj, obj interface{}) {
			newEps, ok := obj.(*discoveryv1.EndpointSlice)
			if !ok {
				w.log.Errorf("Failed to cast Endpointslice")
				return
			}
			oldEps, ok := oldobj.(*discoveryv1.EndpointSlice)
			if !ok {
				w.log.Errorf("Failed to cast Endpointslice")
				return
			}
			// If obj doesnt match the filter return or it doesnt match resource version return
			// https://github.com/kubernetes/client-go/issues/529
			if !w.Filter(newEps.ObjectMeta) {
				return
			}

			if newEps.ResourceVersion == oldEps.ResourceVersion {
				return
			}

			w.handleEpsUpdate(*oldEps.DeepCopy(), *newEps.DeepCopy())
		},
		DeleteFunc: func(obj interface{}) {
			eps, ok := obj.(*discoveryv1.EndpointSlice)
			if !ok {
				w.log.Errorf("Failed to cast Endpointslice")
				return
			}
			// If obj doesnt match the filter return
			if !w.Filter(eps.ObjectMeta) {
				return
			}
			w.handleEpsDelete(*eps.DeepCopy())
		},
	})
}

func (w *Watcher) Filter(obj metav1.ObjectMeta) bool {
	labels := obj.GetLabels()

	// Service should have label: mirrored-service
	if _, ok := labels["mirror.linkerd.io/mirrored-service"]; !ok {
		return false
	}

	// Service should not have label, as it means its not parent target service
	if _, ok := labels["mirror.linkerd.io/headless-mirror-svc-name"]; ok {
		return false
	}

	return true
}

func (w *Watcher) Run(stopCh chan struct{}) {
	// Start all the shared Informers
	w.InformersFactory.Start(stopCh)
	// Wait for the cache sync
	w.InformersFactory.WaitForCacheSync(stopCh)
}
