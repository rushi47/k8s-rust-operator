package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	client "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	TEST_GLOBAL_SVC_NAME = "echo-test-svc-global"
	TEST_NS              = "default"
	TEST_RETRIES         = 5
)

func GetEndpintSlices() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	}

	//use the current context in kubeconfig
	config, err := client.BuildConfigFromFlags("", *kubeconfig)

	fmt.Errorf(err.Error())
	//Make sure config is not nil

	client, err := kubernetes.NewForConfig(config)

	//Make sure global-svc is created.
	_, err = client.CoreV1().Services(TEST_NS).Get(context.Background(), TEST_GLOBAL_SVC_NAME, metav1.GetOptions{})

}

func TestGlobalService(t *testing.T) {
	t.Logf("Testing if Service: %v is created", TEST_GLOBAL_SVC_NAME)

	// Make request && make sure we get response from both cluster in X retries.
	// Add retrieved responses inside map of string and make sure length is 2.
	cluster := make(map[string]string)
	tries := 0
	for tries < TEST_RETRIES {
		tries += 1
		// Try service for couple of times
		url := fmt.Sprintf("http://%v.%v", TEST_GLOBAL_SVC_NAME, TEST_NS)

		resp, err := http.Get(url)

		assert.NoError(t, err, "Unable to make HTTP request")
		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "Unable to read Response body")

		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		_, ok := cluster[cluster_name]
		if !ok {
			cluster[cluster_name] = cluster_name
		}
	}

	//Make sure length of map is 2. This means we got response from both the clusters
	assert.Equal(t, len(cluster), 2, " Looks like Global Service is unable to balance request beetween two clusters.")
}

func TestIndividualCluster(t *testing.T) {

	t.Logf("Test specifc cluster, target1 & target2")

	t.Run("Test Endpoint for Target1", func(t *testing.T) {
		// echo-test-0-k3d-target1.echo-test-svc-global/echo
		url := fmt.Sprintf("http://%v.%v", "echo-test-0-k3d-target1", TEST_GLOBAL_SVC_NAME)

		resp, err := http.Get(url)

		assert.NoError(t, err, "Unable to make HTTP request")
		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "Unable to read Response body")

		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		// Response will have cluster name
		assert.Equal(t, true, strings.Contains(cluster_name, "target1"))

	})

	t.Run("Test Endpoint for Target2", func(t *testing.T) {
		// echo-test-0-k3d-target1.echo-test-svc-global/echo
		url := fmt.Sprintf("http://%v.%v", "echo-test-0-k3d-target2", TEST_GLOBAL_SVC_NAME)

		resp, err := http.Get(url)

		assert.NoError(t, err, "Unable to make HTTP request")
		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err, "Unable to read Response body")

		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		// Response will have cluster name
		assert.Equal(t, true, strings.Contains(cluster_name, "target2"))

	})
}
