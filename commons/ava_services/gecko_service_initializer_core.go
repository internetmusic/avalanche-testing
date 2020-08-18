package ava_services

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/ava-labs/avalanche-e2e-tests/commons/ava_services/cert_providers"
	"github.com/docker/go-connections/nat"
	"github.com/kurtosis-tech/kurtosis/commons/services"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

const (
	httpPort    nat.Port = "9650/tcp"
	stakingPort nat.Port = "9651/tcp"

	stakingTlsCertFileId = "staking-tls-cert"
	stakingTlsKeyFileId  = "staking-tls-key"

	testVolumeMountpoint = "/shared"
	avalancheBinary      = "/gecko/build/avalanche"
)

// ========= Loglevel Enum ========================
type GeckoLogLevel string

const (
	LOG_LEVEL_VERBOSE GeckoLogLevel = "verbo"
	LOG_LEVEL_DEBUG   GeckoLogLevel = "debug"
	LOG_LEVEL_INFO    GeckoLogLevel = "info"
)

// GeckoServiceInitializerCore implements Kurtosis' services.ServiceInitializerCore used to initialize a Gecko service
type GeckoServiceInitializerCore struct {
	// Snow protocol sample size that the Gecko node will be run with
	snowSampleSize int

	// Snow protocol quorum size that the Gecko node will be run with
	snowQuorumSize int

	// Whether the Gecko node will start with TLS staking enabled or not
	stakingEnabled bool

	// TODO Switch these to be named properties of this struct, so that we're being explicit about what arguments
	//  are consumed
	// A set of CLI args that will be passed as-is to the Gecko service
	additionalCLIArgs map[string]string

	// The node IDs of the nodes this node should bootstrap from
	bootstrapperNodeIDs []string

	// Cert provider that should be used when initializing the Gecko service
	certProvider cert_providers.GeckoCertProvider

	// Log level that the Gecko service should start with
	logLevel GeckoLogLevel
}

/*
Creates a new Gecko service initializer core with the following parameters:

Args:
	snowSampleSize: Sample size for Snow consensus protocol
	snowQuroumSize: Quorum size for Snow consensus protocol
	stakingEnabled: Whether this node will use staking
	cliArgs: A mapping of cli_arg -> cli_arg_value that will be passed as-is to the Gecko node
	bootstrapperNodeIDs: The node IDs of the bootstrapper nodes that this node will connect to. While this *seems* unintuitive
		why this would be required, it's because Gecko doesn't actually use certs. So, to prevent against man-in-the-middle attacks,
		the user is required to manually specify the node IDs of the nodese it's connecting to.
	certProvider: Provides the certs used by the Gecko services generated by this core
	logLevel: The loglevel that the Gecko node should output at.

Returns:
	An intializer core for creating Gecko nodes with the specified parameers.
*/
func NewGeckoServiceInitializerCore(
	snowSampleSize int,
	snowQuorumSize int,
	stakingEnabled bool,
	additionalCLIArgs map[string]string,
	bootstrapperNodeIDs []string,
	certProvider cert_providers.GeckoCertProvider,
	logLevel GeckoLogLevel) *GeckoServiceInitializerCore {
	// Defensive copy
	bootstrapperIDsCopy := make([]string, 0, len(bootstrapperNodeIDs))
	for _, nodeID := range bootstrapperNodeIDs {
		bootstrapperIDsCopy = append(bootstrapperIDsCopy, nodeID)
	}

	return &GeckoServiceInitializerCore{
		snowSampleSize:      snowSampleSize,
		snowQuorumSize:      snowQuorumSize,
		stakingEnabled:      stakingEnabled,
		additionalCLIArgs:   additionalCLIArgs,
		bootstrapperNodeIDs: bootstrapperIDsCopy,
		certProvider:        certProvider,
		logLevel:            logLevel,
	}
}

/*
Implementation of services.ServiceInitializerCore function to declare Gecko's used ports
*/
func (core GeckoServiceInitializerCore) GetUsedPorts() map[nat.Port]bool {
	return map[nat.Port]bool{
		httpPort:    true,
		stakingPort: true,
	}
}

/*
Implementation of services.ServiceInitializerCore function to declare the files Gecko needs
*/
func (core GeckoServiceInitializerCore) GetFilesToMount() map[string]bool {
	if core.stakingEnabled {
		return map[string]bool{
			stakingTlsCertFileId: true,
			stakingTlsKeyFileId:  true,
		}
	}
	return make(map[string]bool)
}

/*
Implementation of services.ServiceInitializerCore function to initialize the files Gecko needs
*/
func (core GeckoServiceInitializerCore) InitializeMountedFiles(osFiles map[string]*os.File, dependencies []services.Service) error {
	certFilePointer := osFiles[stakingTlsCertFileId]
	keyFilePointer := osFiles[stakingTlsKeyFileId]
	certPEM, keyPEM, err := core.certProvider.GetCertAndKey()
	if err != nil {
		return stacktrace.Propagate(err, "Could not get cert & key when initializing service")
	}
	if _, err := certFilePointer.Write(certPEM.Bytes()); err != nil {
		return err
	}
	if _, err := keyFilePointer.Write(keyPEM.Bytes()); err != nil {
		return err
	}
	return nil
}

/*
Implementation of services.ServiceInitializerCore function to build the command line that will be used to launch a Gecko service
*/
func (core GeckoServiceInitializerCore) GetStartCommand(mountedFileFilepaths map[string]string, publicIPAddr net.IP, dependencies []services.Service) ([]string, error) {
	numBootNodeIds := len(core.bootstrapperNodeIDs)
	numDependencies := len(dependencies)
	if numDependencies > numBootNodeIds {
		return nil, stacktrace.NewError(
			"Gecko service is being started with %v dependencies but only %v boot node IDs have been configured",
			numDependencies,
			numBootNodeIds,
		)
	}

	publicIPFlag := fmt.Sprintf("--public-ip=%s", publicIPAddr.String())
	commandList := []string{
		avalancheBinary,
		publicIPFlag,
		"--network-id=local",
		fmt.Sprintf("--http-port=%d", httpPort.Int()),
		"--http-host=", // Leave empty to make API openly accessible
		fmt.Sprintf("--staking-port=%d", stakingPort.Int()),
		fmt.Sprintf("--log-level=%s", core.logLevel),
		fmt.Sprintf("--snow-sample-size=%d", core.snowSampleSize),
		fmt.Sprintf("--snow-quorum-size=%d", core.snowQuorumSize),
		fmt.Sprintf("--staking-enabled=%v", core.stakingEnabled),
		fmt.Sprintf("--avax-tx-fee=%d", 0),
	}

	if core.stakingEnabled {
		certFilepath, found := mountedFileFilepaths[stakingTlsCertFileId]
		if !found {
			return nil, stacktrace.NewError("Could not find file key '%v' in the mounted filepaths map; this is likely a code bug", stakingTlsCertFileId)
		}
		keyFilepath, found := mountedFileFilepaths[stakingTlsKeyFileId]
		if !found {
			return nil, stacktrace.NewError("Could not find file key '%v' in the mounted filepaths map; this is likely a code bug", stakingTlsKeyFileId)
		}
		commandList = append(commandList, fmt.Sprintf("--staking-tls-cert-file=%s", certFilepath))
		commandList = append(commandList, fmt.Sprintf("--staking-tls-key-file=%s", keyFilepath))

		// NOTE: This seems weird, BUT there's a reason for it: Gecko doesn't use certs, and instead relies on
		//  the user explicitly passing in the node ID of the bootstrapper it wants. This prevents man-in-the-middle
		//  attacks, just like using a cert would. Us hardcoding this bootstrapper ID here is the equivalent
		//  of a user knowing the node ID in advance, which provides the same level of protection.
		commandList = append(commandList, "--bootstrap-ids="+strings.Join(core.bootstrapperNodeIDs, ","))
	}

	if len(dependencies) > 0 {
		avaDependencies := make([]AvaService, 0, len(dependencies))
		for _, service := range dependencies {
			avaDependencies = append(avaDependencies, service.(AvaService))
		}

		socketStrs := make([]string, 0, len(avaDependencies))
		for _, service := range avaDependencies {
			socket := service.GetStakingSocket()
			socketStrs = append(socketStrs, fmt.Sprintf("%s:%d", socket.GetIpAddr(), socket.GetPort().Int()))
		}
		joinedSockets := strings.Join(socketStrs, ",")
		commandList = append(commandList, "--bootstrap-ips="+joinedSockets)
	}

	// Append additional CLI arguments
	// These are added as is with no additional checking
	for param, argument := range core.additionalCLIArgs {
		commandList = append(commandList, fmt.Sprintf("--%s=%s", param, argument))
	}

	logrus.Debugf("Command list: %+v", commandList)
	return commandList, nil
}

/*
Implementation of services.ServiceInitializerCore function to take the IP address of the Docker container that Kurtosis
	launches Gecko inside and wrap it with our GeckoService implementation of AvaService
*/
func (core GeckoServiceInitializerCore) GetServiceFromIp(ipAddr string) services.Service {
	return GeckoService{
		ipAddr:      ipAddr,
		stakingPort: stakingPort,
		jsonRpcPort: httpPort,
	}
}

/*
Implementation of services.ServiceInitializerCore function to declare the path on the Gecko Docker image where the test
	Docker volume should be mounted on
*/
func (core GeckoServiceInitializerCore) GetTestVolumeMountpoint() string {
	return testVolumeMountpoint
}
