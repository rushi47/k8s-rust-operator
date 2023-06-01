package main

import (
	"context"
	"flag"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	TEST_GLOBAL_SVC_NAME = "nginx-svc-global"
	TEST_NS              = "default"
)

func TestGlobalServiceCreation(t *testing.T) {
	t.Logf("Testing if Service: %v is create", TEST_GLOBAL_SVC_NAME)

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	}

	//use the current context in kubeconfig
	config, err := client.BuildConfigFromFlags("", *kubeconfig)

	//Make sure config is not nil
	assert.NoError(t, err)

	client, err := kubernetes.NewForConfig(config)
	assert.NoError(t, err)

	//Make sure global-svc is created.
	_, err = client.CoreV1().Services(TEST_NS).Get(context.Background(), TEST_GLOBAL_SVC_NAME, metav1.GetOptions{})
	assert.NoError(t, err)

	//Make sure ports exists
}
