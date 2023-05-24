package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	globalMirrorWatcher "github.com/rushi47/service-mirror-prototype/watcher"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Make sure all global services lies in only one namespace.
const GLOBAL_SVC_NAMESPACE = "default"

func main() {
	// Set up logger .
	log := &logrus.Logger{
		Out:   os.Stderr,
		Level: logrus.InfoLevel,
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

	watcher := globalMirrorWatcher.NewWatch(context.Background(), *client, log, *globalSvcNamespace)

	watcher.RegisterHandlers()

	watcher.Run(stopCh)

	// Run the program indefinitely
	<-stopCh
}
