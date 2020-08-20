package services

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/ava-labs/avalanche-e2e-tests/avalanche/services/certs"
	"github.com/docker/go-connections/nat"
	"github.com/kurtosis-tech/kurtosis/commons/services"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

const (
	httpPort    nat.Port = "9650/tcp"
	stakingPort nat.Port = "9651/tcp"

	stakingTLSCertFileID = "staking-tls-cert"
	stakingTLSKeyFileID  = "staking-tls-key"

	testVolumeMountpoint = "/shared"
	avalancheBinary      = "/gecko/build/avalanche"
)

// GeckoLogLevel specifies the log level for a Gecko client
type GeckoLogLevel string

const (
	VERBOSE GeckoLogLevel = "verbo"
	DEBUG   GeckoLogLevel = "debug"
	INFO    GeckoLogLevel = "info"
)

// GeckoServiceInitializerCore implements Kurtosis' services.ServiceInitializerCore used to initialize a Gecko service
type GeckoServiceInitializerCore struct {
	// Snow protocol sample size that the Gecko node will be run with
	snowSampleSize int

	// Snow protocol quorum size that the Gecko node will be run with
	snowQuorumSize int

	// Whether the Gecko node will start with TLS staking enabled or not
	stakingEnabled bool

	// The fixed transaction fee for the network
	txFee uint64

	// TODO Switch these to be named properties of this struct, so that we're being explicit about what arguments
	//  are consumed
	// A set of CLI args that will be passed as-is to the Gecko service
	additionalCLIArgs map[string]string

	// The node IDs of the nodes this node should bootstrap from
	bootstrapperNodeIDs []string

	// Cert provider that should be used when initializing the Gecko service
	certProvider certs.GeckoCertProvider

	// Log level that the Gecko service should start with
	logLevel GeckoLogLevel
}

// NewGeckoServiceInitializerCore creates a new Gecko service initializer core with the following parameters:
// Args:
// 		snowSampleSize: Sample size for Snow consensus protocol
// 		snowQuroumSize: Quorum size for Snow consensus protocol
// 		stakingEnabled: Whether this node will use staking
// 		cliArgs: A mapping of cli_arg -> cli_arg_value that will be passed as-is to the Gecko node
// 		bootstrapperNodeIDs: The node IDs of the bootstrapper nodes that this node will connect to. While this *seems* unintuitive
// 			why this would be required, it's because Gecko doesn't actually use certs. So, to prevent against man-in-the-middle attacks,
// 			the user is required to manually specify the node IDs of the nodese it's connecting to.
// 		certProvider: Provides the certs used by the Gecko services generated by this core
// 		logLevel: The loglevel that the Gecko node should output at.
// Returns:
// 		An intializer core for creating Gecko nodes with the specified parameers.
func NewGeckoServiceInitializerCore(
	snowSampleSize int,
	snowQuorumSize int,
	txFee uint64,
	stakingEnabled bool,
	additionalCLIArgs map[string]string,
	bootstrapperNodeIDs []string,
	certProvider certs.GeckoCertProvider,
	logLevel GeckoLogLevel) *GeckoServiceInitializerCore {
	// Defensive copy
	bootstrapperIDsCopy := make([]string, 0, len(bootstrapperNodeIDs))
	for _, nodeID := range bootstrapperNodeIDs {
		bootstrapperIDsCopy = append(bootstrapperIDsCopy, nodeID)
	}

	return &GeckoServiceInitializerCore{
		snowSampleSize:      snowSampleSize,
		snowQuorumSize:      snowQuorumSize,
		txFee:               txFee,
		stakingEnabled:      stakingEnabled,
		additionalCLIArgs:   additionalCLIArgs,
		bootstrapperNodeIDs: bootstrapperIDsCopy,
		certProvider:        certProvider,
		logLevel:            logLevel,
	}
}

// GetUsedPorts implements services.ServiceInitializerCore to declare Gecko's used ports
func (core GeckoServiceInitializerCore) GetUsedPorts() map[nat.Port]bool {
	return map[nat.Port]bool{
		httpPort:    true,
		stakingPort: true,
	}
}

// GetFilesToMount implements services.ServiceInitializerCore to declare the files Gecko needs
func (core GeckoServiceInitializerCore) GetFilesToMount() map[string]bool {
	if core.stakingEnabled {
		return map[string]bool{
			stakingTLSCertFileID: true,
			stakingTLSKeyFileID:  true,
		}
	}
	return make(map[string]bool)
}

// InitializeMountedFiles implementats services.ServiceInitializerCore to initialize the files Gecko needs
func (core GeckoServiceInitializerCore) InitializeMountedFiles(osFiles map[string]*os.File, dependencies []services.Service) error {
	certFilePointer := osFiles[stakingTLSCertFileID]
	keyFilePointer := osFiles[stakingTLSKeyFileID]
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

// GetStartCommand implements services.ServiceInitializerCore to build the command line that will be used to launch a Gecko service
func (core GeckoServiceInitializerCore) GetStartCommand(mountedFileFilepaths map[string]string, publicIPAddr net.IP, dependencies []services.Service) ([]string, error) {
	numBootNodeIDs := len(core.bootstrapperNodeIDs)
	numDependencies := len(dependencies)
	if numDependencies > numBootNodeIDs {
		return nil, stacktrace.NewError(
			"Gecko service is being started with %v dependencies but only %v boot node IDs have been configured",
			numDependencies,
			numBootNodeIDs,
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
		fmt.Sprintf("--tx-fee=%d", core.txFee),
	}

	if core.stakingEnabled {
		certFilepath, found := mountedFileFilepaths[stakingTLSCertFileID]
		if !found {
			return nil, stacktrace.NewError("Could not find file key '%v' in the mounted filepaths map; this is likely a code bug", stakingTLSCertFileID)
		}
		keyFilepath, found := mountedFileFilepaths[stakingTLSKeyFileID]
		if !found {
			return nil, stacktrace.NewError("Could not find file key '%v' in the mounted filepaths map; this is likely a code bug", stakingTLSKeyFileID)
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
		avaDependencies := make([]AvalancheService, 0, len(dependencies))
		for _, service := range dependencies {
			avaDependencies = append(avaDependencies, service.(AvalancheService))
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

// GetServiceFromIp implements services.ServiceInitializerCore function to take the IP address of the Docker container that Kurtosis
// launches Gecko inside and wrap it with our GeckoService implementation of AvalancheService
func (core GeckoServiceInitializerCore) GetServiceFromIp(ipAddr string) services.Service {
	return GeckoService{
		ipAddr:      ipAddr,
		stakingPort: stakingPort,
		jsonRPCPort: httpPort,
	}
}

// GetTestVolumeMountpoint implements services.ServiceInitializerCore to declare the path on the Gecko Docker image where the test
// Docker volume should be mounted on
func (core GeckoServiceInitializerCore) GetTestVolumeMountpoint() string {
	return testVolumeMountpoint
}