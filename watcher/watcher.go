package watcher

import (
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
}

func NewWatch(client kubernetes.Clientset, log *logrus.Logger, namespace string) Watcher {
	watch := &Watcher{
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
			//If obj doesnt match the filter return
			if (ok) && !w.Filter(service.ObjectMeta) {
				return
			}
			w.log.Debugf("New Service Added : %v", service.Name)
		},
	})

	//Create Informer for endpointslice
	epsInformer := w.InformersFactory.Discovery().V1().EndpointSlices().Informer()
	epsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			eps, ok := obj.(*discoveryv1.EndpointSlice)
			//If obj doesnt match the filter return
			if ok && !w.Filter(eps.ObjectMeta) {
				return
			}
			w.log.Infof("New Eps Added : %v", eps.Name)
		},
	})
}

func (w *Watcher) Filter(obj metav1.ObjectMeta) bool {
	labels := obj.GetLabels()

	//Service should have label: mirrored-service
	if _, ok := labels["mirror.linkerd.io/mirrored-service"]; !ok {
		return false
	}

	//Service should not have label, as it means its not parent target service
	if _, ok := labels["mirror.linkerd.io/headless-mirror-svc-name"]; ok {
		return false
	}

	return true
}

func (w *Watcher) Run(stopCh chan struct{}) {
	//Start all the shared Informers
	w.InformersFactory.Start(stopCh)
	//Wait for the cache sync
	w.InformersFactory.WaitForCacheSync(stopCh)
}
