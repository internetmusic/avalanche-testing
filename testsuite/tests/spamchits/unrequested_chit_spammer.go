package spamchits

import (
	"strconv"
	"time"

	"github.com/kurtosis-tech/kurtosis-go/lib/networks"
	"github.com/kurtosis-tech/kurtosis-go/lib/testsuite"

	avalancheNetwork "github.com/ava-labs/avalanche-testing/avalanche/networks"
	avalancheService "github.com/ava-labs/avalanche-testing/avalanche/services"
	"github.com/ava-labs/avalanche-testing/testsuite/helpers"
	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

const (
	normalNodeConfigID     networks.ConfigurationID = "normal-config"
	byzantineConfigID      networks.ConfigurationID = "byzantine-config"
	byzantineUsername                               = "byzantine_avalanche"
	byzantinePassword                               = "byzant1n3!"
	stakerUsername                                  = "staker_avalanche"
	stakerPassword                                  = "test34test!23"
	normalNodeServiceID    networks.ServiceID       = "normal-node"
	byzantineNodePrefix    string                   = "byzantine-node-"
	numberOfByzantineNodes                          = 4
	seedAmount                                      = uint64(50000000000000)
	stakeAmount                                     = uint64(30000000000000)

	networkAcceptanceTimeoutRatio = 0.3
	byzantineBehavior             = "byzantine-behavior"
	chitSpammerBehavior           = "chit-spammer"
)

// StakingNetworkUnrequestedChitSpammerTest tests that a node is able to continue to work normally
// while the network is spammed with chit messages from byzantine peers
type StakingNetworkUnrequestedChitSpammerTest struct {
	ByzantineImageName string
	NormalImageName    string
}

// Run implements the Kurtosis Test interface
func (test StakingNetworkUnrequestedChitSpammerTest) Run(network networks.Network, context testsuite.TestContext) {
	castedNetwork := network.(avalancheNetwork.TestAvalancheNetwork)
	networkAcceptanceTimeout := time.Duration(networkAcceptanceTimeoutRatio * float64(test.GetExecutionTimeout().Nanoseconds()))

	// ============= ADD SET OF BYZANTINE NODES AS VALIDATORS ON THE NETWORK ===================
	logrus.Infof("Adding byzantine chit spammer nodes as stakers...")
	for i := 0; i < numberOfByzantineNodes; i++ {
		byzClient, err := castedNetwork.GetAvalancheClient(networks.ServiceID(byzantineNodePrefix + strconv.Itoa(i)))
		if err != nil {
			context.Fatal(stacktrace.Propagate(err, "Failed to get byzantine client."))
		}
		highLevelByzClient := helpers.NewRPCWorkFlowRunner(
			byzClient,
			api.UserPass{Username: byzantineUsername, Password: byzantinePassword},
			networkAcceptanceTimeout)
		_, err = highLevelByzClient.ImportGenesisFundsAndStartValidating(seedAmount, stakeAmount)
		if err != nil {
			context.Fatal(stacktrace.Propagate(err, "Failed add client as a validator."))
		}
		currentStakers, err := byzClient.PChainAPI().GetCurrentValidators(ids.Empty)
		if err != nil {
			context.Fatal(stacktrace.Propagate(err, "Could not get current stakers."))
		}
		logrus.Infof("Current Stakers: %d", len(currentStakers))
	}

	// =================== ADD NORMAL NODE AS A VALIDATOR ON THE NETWORK =======================
	logrus.Infof("Adding normal node as a staker...")
	availabilityChecker, err := castedNetwork.AddService(normalNodeConfigID, normalNodeServiceID)
	if err != nil {
		context.Fatal(stacktrace.Propagate(err, "Failed to add normal node with high quorum and sample to network."))
	}
	if err = availabilityChecker.WaitForStartup(); err != nil {
		context.Fatal(stacktrace.Propagate(err, "Failed to wait for startup of normal node."))
	}
	normalClient, err := castedNetwork.GetAvalancheClient(normalNodeServiceID)
	if err != nil {
		context.Fatal(stacktrace.Propagate(err, "Failed to get staker client."))
	}
	highLevelNormalClient := helpers.NewRPCWorkFlowRunner(
		normalClient,
		api.UserPass{Username: stakerUsername, Password: stakerPassword},
		networkAcceptanceTimeout)
	_, err = highLevelNormalClient.ImportGenesisFundsAndStartValidating(seedAmount, stakeAmount)
	if err != nil {
		context.Fatal(stacktrace.Propagate(err, "Failed to add client as a validator."))
	}

	logrus.Infof("Added normal node as a staker. Sleeping an additional 10 seconds to ensure it joins current validators...")
	time.Sleep(10 * time.Second)

	// ============= VALIDATE NETWORK STATE DESPITE BYZANTINE BEHAVIOR =========================
	logrus.Infof("Validating network state...")
	currentStakers, err := normalClient.PChainAPI().GetCurrentValidators(ids.Empty)
	if err != nil {
		context.Fatal(stacktrace.Propagate(err, "Could not get current stakers."))
	}
	actualNumStakers := len(currentStakers)
	expectedNumStakers := 10
	logrus.Debugf("Number of current stakers: %d, expected number of stakers: %d", actualNumStakers, expectedNumStakers)
	if actualNumStakers != expectedNumStakers {
		context.AssertTrue(actualNumStakers == expectedNumStakers, stacktrace.NewError("Actual number of stakers, %v, != expected number of stakers, %v", actualNumStakers, expectedNumStakers))
	}
}

// GetNetworkLoader implements the Kurtosis Test interface
func (test StakingNetworkUnrequestedChitSpammerTest) GetNetworkLoader() (networks.NetworkLoader, error) {
	// Define normal node and byzantine node configurations
	byzantineServiceConfig := *avalancheNetwork.NewAvalancheByzantineServiceConfig(test.ByzantineImageName, chitSpammerBehavior)
	bootstrapServiceConfig := *avalancheNetwork.NewDefaultAvalancheNetworkServiceConfig(test.NormalImageName)
	serviceConfigs := map[networks.ConfigurationID]avalancheNetwork.TestAvalancheNetworkServiceConfig{
		byzantineConfigID: byzantineServiceConfig,
		normalNodeConfigID: *avalancheNetwork.NewTestAvalancheNetworkServiceConfig(
			true,
			avalancheService.DEBUG,
			test.NormalImageName,
			6,
			8,
			2*time.Second,
			make(map[string]string),
		),
	}

	// Define the map from service->configuration for the network
	serviceIDConfigMap := map[networks.ServiceID]networks.ConfigurationID{}
	for i := 0; i < numberOfByzantineNodes; i++ {
		serviceIDConfigMap[networks.ServiceID(byzantineNodePrefix+strconv.Itoa(i))] = byzantineConfigID
	}
	logrus.Debugf("Byzantine Image Name: %s", test.ByzantineImageName)
	logrus.Debugf("Normal Image Name: %s", test.NormalImageName)

	return avalancheNetwork.NewTestAvalancheNetworkLoader(
		true,
		0,
		bootstrapServiceConfig,
		serviceConfigs,
		serviceIDConfigMap,
	)
}

// GetExecutionTimeout implements the Kurtosis Test interface
func (test StakingNetworkUnrequestedChitSpammerTest) GetExecutionTimeout() time.Duration {
	// TODO drop this when the availabilityChecker doesn't have a sleep, because we spin up a *bunch* of byzantine
	// nodes during test execution
	return 10 * time.Minute
}

// GetSetupBuffer implements the Kurtosis Test interface
func (test StakingNetworkUnrequestedChitSpammerTest) GetSetupBuffer() time.Duration {
	return 4 * time.Minute
}
