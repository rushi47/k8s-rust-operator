package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Make sure all global services lies in only one namespace.
const GLOBAL_SVC_NAMESPACE = "default"

type Context struct {
	ctx    context.Context
	log    logrus.Logger
	client kubernetes.Clientset
}

// Struct to build service watcher which looks after target services.
type ServiceWatcher struct {
	ctx       Context
	svcFilter labels.Selector
	informer  cache.SharedInformer
	// Name space where to install service watcher,
	namespace string
}

type GlobalServiceMirrorInformers struct {
	//Service handle rinformer
	svcInformer cache.SharedInformer
	//Endpoint handler informer
	// epInformer cache.ResourceEventHandler
}

func NewServiceWatcher(ctx Context, ns string) ServiceWatcher {
	svc := &ServiceWatcher{
		ctx: ctx,
	}
	svc.svcFilter = svc.createServiceFilter()
	svc.informer = svc.createSharedInformer()
	svc.namespace = ns
	return *svc
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
	globalSvcNs := *flag.String("globalsvc-ns", GLOBAL_SVC_NAMESPACE, "(optional) Namespace to install service mirror controller and global mirror services.")

	flag.Parse()

	// use the current context in kubeconfig
	config, err := client.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Panicf("Probably running Inside Cluster :%v", err.Error())
	}

	// creates the clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Panicf("Issue in building client from config : %v", err.Error())
	}

	//Create context
	ctx := &Context{
		ctx:    context.TODO(),
		log:    *log,
		client: *client,
	}

	//Make sure it runs in loop.
	stopCh := make(chan struct{})
	defer close(stopCh)

	svcWatcher := NewServiceWatcher(*ctx, globalSvcNs)

	//Build global informer
	globaMirrorInformer := GlobalServiceMirrorInformers{
		svcInformer: svcWatcher.informer,
	}

	go globaMirrorInformer.svcInformer.Run(stopCh)

	// Wait until the informer is synced
	if !cache.WaitForCacheSync(stopCh, globaMirrorInformer.svcInformer.HasSynced) {
		log.Panicln("Failed to sync informer cache")
	}

	// Run the program indefinitely
	<-stopCh
}
