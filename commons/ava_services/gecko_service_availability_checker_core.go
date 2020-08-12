package ava_services

import (
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-e2e-tests/gecko_client/apis"
	"github.com/ava-labs/avalanche-e2e-tests/utils/constants"
	"github.com/kurtosis-tech/kurtosis/commons/services"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

/*
An implementation of services.ServiceAvailabilityCheckerCore that defines the criteria for a Gecko service being available
*/
type GeckoServiceAvailabilityCheckerCore struct{}

/*
An implementation of services.ServiceAvailabilityCheckerCore#IsServiceUp that returns true when the Gecko healthcheck
	reports that the node is available
*/
func (g GeckoServiceAvailabilityCheckerCore) IsServiceUp(toCheck services.Service, dependencies []services.Service) bool {
	// NOTE: we don't check the dependencies intentionally, because we don't need to - a Gecko service won't report itself
	//  as up until its bootstrappers are up

	castedService := toCheck.(GeckoService)
	jsonRpcSocket := castedService.GetJsonRpcSocket()
	uri := fmt.Sprintf("http://%s:%d", jsonRpcSocket.GetIpAddr(), jsonRpcSocket.GetPort().Int())
	client := apis.NewClient(uri, constants.DefaultRequestTimeout)
	healthInfo, err := client.HealthAPI().GetLiveness()
	if err != nil {
		logrus.Trace(stacktrace.Propagate(err, "Error occurred getting liveness info"))
		return false
	}

	// HACK we need to wait for bootstrapping to finish, and there is not API for this yet (in development)
	// TODO once isReadiness endpoint is available, use that instead of just waiting
	if healthInfo.Healthy {
		time.Sleep(15 * time.Second)
	}

	return healthInfo.Healthy
}

func (g GeckoServiceAvailabilityCheckerCore) GetTimeout() time.Duration {
	return 90 * time.Second
}
