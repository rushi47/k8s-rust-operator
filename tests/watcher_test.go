package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	TEST_GLOBAL_SVC_NAME   = "echo-test-svc-global"
	TEST_NS                = "default"
	TES_MAX_RETRY_DURATION = 3 // Seconds
	TEST_MAX_RETRIES       = 5
)

func httpGetWithRetry(timeout time.Duration, url string) (*http.Response, error) {

	resp, err := http.Get(url)
	if err == nil {
		return resp, nil
	}

	after := time.NewTimer(timeout)
	defer after.Stop()
	retryAfter := time.NewTicker(time.Second)
	defer retryAfter.Stop()

	for {
		select {
		case <-after.C:
			return resp, err

		case <-retryAfter.C:
			resp, err := http.Get(url)
			if err == nil {
				return resp, nil
			}
		}
	}
}

func TestGlobalService(t *testing.T) {

	// Make request && make sure we get response from both cluster in X retries.
	// Add retrieved responses inside map of string and make sure length is 2.
	cluster := make(map[string]string)

	// Try service for couple of times
	url := fmt.Sprintf("http://%v.%v", TEST_GLOBAL_SVC_NAME, TEST_NS)

	for attempt := 0; attempt < TEST_MAX_RETRIES; attempt++ {

		resp, err := httpGetWithRetry(TES_MAX_RETRY_DURATION, url)

		if err != nil {
			t.Logf("Issue in connecting to url: %v, err: %v", url, err.Error())
			// Retry
			continue
		}

		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			// Retry again
			continue
		}

		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		_, ok := cluster[cluster_name]
		if !ok {
			cluster[cluster_name] = cluster_name
		}
	}
	//Make sure length of map is 2. It suggests that the request was balanced between two clusters, but the test is flaky.
	assert.Equal(t, len(cluster), 2, " Looks like Global Service is unable to balance request beetween two clusters.")
}

func TestIndividualCluster(t *testing.T) {

	t.Run("Test Endpoint for Target1", func(t *testing.T) {
		// echo-test-0-k3d-target1.echo-test-svc-global/echo
		url := fmt.Sprintf("http://%v.%v", "echo-test-0-k3d-target1", TEST_GLOBAL_SVC_NAME)

		resp, err := httpGetWithRetry(TES_MAX_RETRY_DURATION, url)

		if err != nil {
			t.Fatalf("Issue in make request: %v", err.Error())
		}
		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Issue in reading response body: %v", err.Error())
		}

		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		// Response will have cluster name
		assert.Equal(t, true, strings.Contains(cluster_name, "target1"))

	})

	t.Run("Test Endpoint for Target2", func(t *testing.T) {
		// echo-test-0-k3d-target1.echo-test-svc-global/echo
		url := fmt.Sprintf("http://%v.%v", "echo-test-0-k3d-target2", TEST_GLOBAL_SVC_NAME)

		resp, err := httpGetWithRetry(TES_MAX_RETRY_DURATION, url)

		if err != nil {
			t.Fatalf("Issue in make request: %v", err.Error())
		}

		defer resp.Body.Close()

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Issue in reading response body: %v", err.Error())
		}
		respBody := string(body)
		cluster_name := strings.Split(respBody, "Hello from: ")[1]

		// Response will have cluster name
		assert.Equal(t, true, strings.Contains(cluster_name, "target2"))

	})
}
