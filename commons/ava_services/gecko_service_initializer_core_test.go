package ava_services

import (
	"github.com/kurtosis-tech/kurtosis/commons/services"
	"gotest.tools/assert"
	"testing"
)


const TEST_IP="172.17.0.2"


func TestGetContainerStartCommand(t *testing.T) {
	initializerConfig := GeckoServiceInitializerCore{
		snowSampleSize:    1,
		snowQuorumSize:    1,
		stakingTlsEnabled: false,
		logLevel:          LOG_LEVEL_INFO,
	}

	expectedNoDeps := []string{
		"/gecko/build/ava",
		"--public-ip=" + TEST_IP,
		"--network-id=local",
		"--http-port=9650",
		"--staking-port=9651",
		"--log-level=info",
		"--snow-sample-size=1",
		"--snow-quorum-size=1",
		"--staking-tls-enabled=false",
	}
	actualNoDeps, err := initializerConfig.GetStartCommand(TEST_IP, make([]services.Service, 0))
	if err != nil {
		panic(err)
	}
	assert.DeepEqual(t, expectedNoDeps, actualNoDeps)

	testDependency := GeckoService{ipAddr: "1.2.3.4"}
	testDependencySlice := []services.Service{
		testDependency,
	}
	expectedWithDeps := append(expectedNoDeps, "--bootstrap-ips=1.2.3.4:9651")
	actualWithDeps, err := initializerConfig.GetStartCommand(TEST_IP, testDependencySlice)
	if err != nil {
		panic(err)
	}
	assert.DeepEqual(t, expectedWithDeps, actualWithDeps)

}