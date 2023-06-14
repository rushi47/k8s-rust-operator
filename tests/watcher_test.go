package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	TEST_GLOBAL_SVC_NAME = "echo-test-svc-global"
	TEST_NS              = "default"
	MAX_RETRIES          = 5
)

func TestGlobalService(t *testing.T) {

	// Make request && make sure we get response from both cluster in X retries.
	// Add retrieved responses inside map of string and make sure length is 2.
	cluster := make(map[string]string)

	// Try service for couple of times
	url := fmt.Sprintf("http://%v.%v", TEST_GLOBAL_SVC_NAME, TEST_NS)

	for attempt := 0; attempt < MAX_RETRIES; attempt++ {

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
