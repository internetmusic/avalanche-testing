package services

import (
	"github.com/kurtosis-tech/kurtosis-go/lib/services"
)

// NodeService implements the Kurtosis generic services.Service interface that represents the minimum interface for a
// validator node
type NodeService interface {
	services.Service

	// GetStakingSocket returns the socket used for communication between nodes on the network
	GetStakingSocket() ServiceSocket
}

// Config ...
type Config interface {
	// GetStartCommand returns the command to start the node
	GetStartCommand() string
	// StartUpOptions returns the config options
	// as a string to be written to a config file
	StartUpOptions() string
}

// AvalancheNode ...
type AvalancheNode interface {
	Configuration() Config
	Initialize() error
	Start() error
	AwaitStartup() error
	GetAPIClient() (interface{}, error)
}

// StaticAvalancheNetwork ...
type StaticAvalancheNetwork interface {
	StartNetwork() error
	GetAvalancheNodes() []AvalancheNode
	GetNode(name string) AvalancheNode
}

// DynamicAvalancheNetwork ...
type DynamicAvalancheNetwork interface {
	StaticAvalancheNetwork
	AddNodeWithConfig(name string, config Config) (AvalancheNode, error)
	RemoveNode(name string) error
}

// A Test will own a network - either a Static or Dynamic Network
// The network will start up
// Some tests will only need access to a single node
// these can be simplified and easily run in any context
