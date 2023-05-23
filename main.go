package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	globalMirrorWatcher "github.com/rushi47/service-mirror-prototype/watcher"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Make sure all global services lies in only one namespace.
const GLOBAL_SVC_NAMESPACE = "default"

type Watcher struct {
	//Various informers like Service, EndpointSlice.
	Informers []cache.SharedInformer
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

	log.Info("Starting Global Mirror")

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	}

	//Specify the NameSpace for install controller & global svc.
	globalSvcNamespace := flag.String("globalsvc-ns", GLOBAL_SVC_NAMESPACE, "(optional) Namespace to install service mirror controller and global mirror services.")

	flag.Parse()

	// use the current context in kubeconfig
	config, err := client.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Panicf("Probably running Inside Cluster: %v", err)
	}

	// creates the clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Panicf("Issue in building client from config: %v", err)
	}

	//Make sure it runs in loop.
	stopCh := make(chan struct{})
	defer close(stopCh)

	watcher := &Watcher{}
	ctx := context.Background()
	// Get the service watcher
	svcWatcher := globalMirrorWatcher.NewServiceWatcher(ctx, client, log, globalSvcNamespace)

	// Get the endpointslice watcher
	epsWatcher := globalMirrorWatcher.NewEndpointSlicesWatcher(ctx, client, log, globalSvcNamespace)

	//Add all the watcher
	watcher.Informers = append(watcher.Informers, svcWatcher.Informer, epsWatcher.Informer)

	//Run all the informers
	for _, informer := range watcher.Informers {
		go informer.Run(stopCh)
	}

	log.Info("Checking informer cache....")
	//Make sure cache is synced.
	for _, informer := range watcher.Informers {
		if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
			log.Error("Failed to sync informer cache")
		}
	}
	// Run the program indefinitely
	<-stopCh
}
