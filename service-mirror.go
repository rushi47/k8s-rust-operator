package main

import (
	"context"
	ctx "context"
	"flag"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Make sure all global services lies in only one namespace.
const GLOBAL_SVC_NAMESPACE = "default"

type Context struct {
	ctx.Context
	kubernetes.Clientset
}

type Watcher struct {
	//Various informers like Service, EndpointSlice.
	informers []cache.SharedInformer
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
	globalSvcNs := flag.String("globalsvc-ns", GLOBAL_SVC_NAMESPACE, "(optional) Namespace to install service mirror controller and global mirror services.")

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

	//Create context
	ctx := &Context{
		context.TODO(),
		*client,
	}

	//Make sure it runs in loop.
	stopCh := make(chan struct{})
	defer close(stopCh)

	svcWatcher := NewServiceWatcher(*ctx, log, globalSvcNs)

	watcher := &Watcher{}
	watcher.informers = append(watcher.informers, svcWatcher.informer)

	//Run all the informers
	for _, informer := range watcher.informers {
		go informer.Run(stopCh)
	}

	//Make sure cache is synced.
	for _, informer := range watcher.informers {
		if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
			log.Panicln("Failed to sync informer cache")
		}
	}
	// Run the program indefinitely
	<-stopCh
}
