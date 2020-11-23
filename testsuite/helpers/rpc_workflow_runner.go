package helpers

import (
	"time"

	avalancheNetwork "github.com/ava-labs/avalanche-testing/avalanche/networks"
	"github.com/ava-labs/avalanche-testing/avalanche/services"
	"github.com/ava-labs/avalanche-testing/utils/constants"
	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

const (
	AvaxAssetID                         = "AVAX"
	DefaultStakingDelay                 = 20 * time.Second
	DefaultStakingPeriod                = 72 * time.Hour
	DefaultDelegationDelay              = 20 * time.Second // Time until delegation period should begin
	stakingPeriodSynchronyDelay         = 3 * time.Second
	DefaultDelegationPeriod             = 36 * time.Hour
	DefaultDelegationFeeRate    float32 = 2
)

// RPCWorkFlowRunner executes standard testing workflows like funding accounts from
// genesis and adding nodes as validators, using the a given avalanche client handle as the
// entry point to the test network. It runs the RpcWorkflows using the credential
// set in the userPass field.
// Note: RPCWorkFlowRunner does not store user credentials in a secure way. It is
// only suitable for testing purposes.
type RPCWorkFlowRunner struct {
	client   *services.Client
	userPass api.UserPass

	// This timeout represents the time the RPCWorkFlowRunner will wait for some state change to be accepted
	// and implemented by the underlying client.
	networkAcceptanceTimeout time.Duration
}

// NewRPCWorkFlowRunner ...
func NewRPCWorkFlowRunner(
	client *services.Client,
	user api.UserPass,
	networkAcceptanceTimeout time.Duration) *RPCWorkFlowRunner {
	return &RPCWorkFlowRunner{
		client:                   client,
		userPass:                 user,
		networkAcceptanceTimeout: networkAcceptanceTimeout,
	}
}

// User returns the user credentials for this worker
func (runner RPCWorkFlowRunner) User() api.UserPass {
	return runner.userPass
}

// ImportGenesisFunds imports the genesis private key to this user's keystore
func (runner RPCWorkFlowRunner) ImportGenesisFunds() (string, error) {
	client := runner.client
	keystore := client.KeystoreAPI()
	if _, err := keystore.CreateUser(runner.userPass); err != nil {
		return "", err
	}

	genesisAccountAddress, err := client.XChainAPI().ImportKey(
		runner.userPass,
		avalancheNetwork.DefaultLocalNetGenesisConfig.FundedAddresses.PrivateKey)
	if err != nil {
		return "", stacktrace.Propagate(err, "Failed to take control of genesis account.")
	}
	logrus.Debugf("Genesis Address: %s.", genesisAccountAddress)
	return genesisAccountAddress, nil
}

// ImportGenesisFundsAndStartValidating attempts to import genesis funds and add this node as a validator
func (runner RPCWorkFlowRunner) ImportGenesisFundsAndStartValidating(
	seedAmount uint64,
	stakeAmount uint64) (string, error) {
	client := runner.client
	stakerNodeID, err := client.InfoAPI().GetNodeID()
	if err != nil {
		return "", stacktrace.Propagate(err, "Could not get staker node ID.")
	}
	_, err = runner.ImportGenesisFunds()
	if err != nil {
		return "", stacktrace.Propagate(err, "Could not seed XChain account from Genesis.")
	}
	pChainAddress, err := client.PChainAPI().CreateAddress(runner.userPass)
	if err != nil {
		return "", stacktrace.Propagate(err, "Failed to create new address on PChain")
	}
	err = runner.TransferAvaXChainToPChain(pChainAddress, seedAmount)
	if err != nil {
		return "", stacktrace.Propagate(err, "Could not transfer AVAX from XChain to PChain account information")
	}
	// Adding staker
	err = runner.AddValidatorToPrimaryNetwork(stakerNodeID, pChainAddress, stakeAmount)
	if err != nil {
		return "", stacktrace.Propagate(err, "Could not add staker %s to primary network.", stakerNodeID)
	}
	return pChainAddress, nil
}

// AddDelegatorToPrimaryNetwork delegates to [delegateeNodeID] and blocks until the transaction is confirmed and the delegation
// period begins
func (runner RPCWorkFlowRunner) AddDelegatorToPrimaryNetwork(
	delegateeNodeID string,
	pChainAddress string,
	stakeAmount uint64,
) error {
	client := runner.client
	delegatorStartTime := time.Now().Add(DefaultDelegationDelay)
	startTime := uint64(delegatorStartTime.Unix())
	endTime := uint64(delegatorStartTime.Add(DefaultDelegationPeriod).Unix())
	addDelegatorTxID, err := client.PChainAPI().AddDelegator(
		runner.userPass,
		pChainAddress,
		delegateeNodeID,
		stakeAmount,
		startTime,
		endTime,
		nil, // from addrs
		"",  // change addr
	)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to add delegator %s", pChainAddress)
	}
	if err := runner.waitForPChainTransactionAcceptance(addDelegatorTxID); err != nil {
		return stacktrace.Propagate(err, "Failed to accept AddDelegator tx: %s", addDelegatorTxID)
	}

	// Sleep until delegator starts validating
	time.Sleep(time.Until(delegatorStartTime) + stakingPeriodSynchronyDelay)
	return nil
}

// AddValidatorToPrimaryNetwork adds [nodeID] as a validator and blocks until the transaction is confirmed and the validation
// period begins
func (runner RPCWorkFlowRunner) AddValidatorToPrimaryNetwork(
	nodeID string,
	pchainAddress string,
	stakeAmount uint64,
) error {
	// Replace with simple call to AddValidator
	client := runner.client
	stakingStartTime := time.Now().Add(DefaultStakingDelay)
	startTime := uint64(stakingStartTime.Unix())
	endTime := uint64(stakingStartTime.Add(DefaultStakingPeriod).Unix())
	addStakerTxID, err := client.PChainAPI().AddValidator(
		runner.userPass,
		pchainAddress,
		nodeID,
		stakeAmount,
		startTime,
		endTime,
		DefaultDelegationFeeRate,
	)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to add validator to primrary network %s", nodeID)
	}

	if err := runner.waitForPChainTransactionAcceptance(addStakerTxID); err != nil {
		return stacktrace.Propagate(err, "Failed to confirm AddValidator Tx: %s", addStakerTxID)
	}

	time.Sleep(time.Until(stakingStartTime) + stakingPeriodSynchronyDelay)

	return nil
}

// FundXChainAddresses sends [amount] AVAX to each address in [addresses] and returns the created txIDs
func (runner RPCWorkFlowRunner) FundXChainAddresses(addresses []string, amount uint64) error {
	client := runner.client.XChainAPI()
	for _, address := range addresses {
		txID, err := client.Send(
			runner.userPass,
			amount,
			AvaxAssetID,
			address,
			nil, // from addrs
			"",  // change addr
		)
		if err != nil {
			return err
		}
		if err := runner.waitForXchainTransactionAcceptance(txID); err != nil {
			return err
		}
	}

	return nil
}

// SendAVAX attempts to send [amount] AVAX to address [to] using [runner]'s userPass
func (runner RPCWorkFlowRunner) SendAVAX(to string, amount uint64) (ids.ID, error) {
	return runner.client.XChainAPI().Send(
		runner.userPass,
		amount,
		AvaxAssetID,
		to,
		nil, // from addrs
		"",  // change addr
	)
}

// CreateDefaultAddresses creates the keystore user for this workflow runner and
// creates an X and P Chain address for that keystore user
func (runner RPCWorkFlowRunner) CreateDefaultAddresses() (string, string, error) {
	client := runner.client
	keystore := client.KeystoreAPI()
	if _, err := keystore.CreateUser(runner.userPass); err != nil {
		return "", "", err
	}

	xAddress, err := client.XChainAPI().CreateAddress(runner.userPass)
	if err != nil {
		return "", "", err
	}

	pAddress, err := client.PChainAPI().CreateAddress(runner.userPass)
	return xAddress, pAddress, err
}

// SendAVAXBackAndForth sends [amount] AVAX to address [to] using funds from [runner.userPass], [numTxs] times
func (runner RPCWorkFlowRunner) SendAVAXBackAndForth(to string, amount, txFee, numTxs uint64, errs chan error) {
	client := runner.client.XChainAPI()

	for i := uint64(1); i < numTxs; i++ {
		txID, err := client.Send(
			runner.userPass,
			amount-txFee*uint64(i),
			AvaxAssetID,
			to,
			nil, // from addrs
			"",  // change addr
		)
		if err != nil {
			errs <- stacktrace.Propagate(err, "Failed to send transaction.")
		}
		if err := runner.waitForXchainTransactionAcceptance(txID); err != nil {
			errs <- stacktrace.Propagate(err, "Failed to await transaction acceptance.")
		}
		logrus.Infof("Confirmed Tx: %s", txID)
	}
	errs <- nil
}

// TransferAvaXChainToPChain exports AVAX from the X Chain and then imports it to the P Chain
// and blocks until both transactions have been accepted
func (runner RPCWorkFlowRunner) TransferAvaXChainToPChain(pChainAddress string, amount uint64) error {
	client := runner.client
	txID, err := client.XChainAPI().ExportAVAX(
		runner.userPass,
		amount,
		pChainAddress,
		nil, // from addrs
		"",  // change addr
	)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to export AVAX to pchainAddress %s", pChainAddress)
	}
	err = runner.waitForXchainTransactionAcceptance(txID)
	if err != nil {
		return stacktrace.Propagate(err, "")
	}

	importTxID, err := client.PChainAPI().ImportAVAX(
		runner.userPass,
		pChainAddress,
		constants.XChainID.String(),
		nil, // from addrs
		"",  // change addr
	)
	if err != nil {
		return stacktrace.Propagate(err, "Failed import AVAX to pchainAddress %s", pChainAddress)
	}
	if err := runner.waitForPChainTransactionAcceptance(importTxID); err != nil {
		return stacktrace.Propagate(err, "Failed to Accept ImportTx: %s", importTxID)
	}

	return nil
}

// TransferAvaPChainToXChain exports AVAX from the P Chain and then imports it to the X Chain
// and blocks until both transactions have been accepted
func (runner RPCWorkFlowRunner) TransferAvaPChainToXChain(
	xChainAddress string,
	amount uint64) error {
	client := runner.client

	exportTxID, err := client.PChainAPI().ExportAVAX(
		runner.userPass,
		xChainAddress,
		amount,
		nil, // from addrs
		"",  // change addr
	)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to export AVAX to xChainAddress %s", xChainAddress)
	}
	if err := runner.waitForPChainTransactionAcceptance(exportTxID); err != nil {
		return stacktrace.Propagate(err, "Failed to accept ExportTx: %s", exportTxID)
	}

	txID, err := client.XChainAPI().ImportAVAX(runner.userPass, xChainAddress, constants.PlatformChainID.String())
	err = runner.waitForXchainTransactionAcceptance(txID)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to wait for acceptance of transaction on XChain.")
	}
	return nil
}

// IssueTxList issues each consecutive transaction in order
func (runner RPCWorkFlowRunner) IssueTxList(
	txList [][]byte,
) error {
	xChainAPI := runner.client.XChainAPI()
	for _, txBytes := range txList {
		_, err := xChainAPI.IssueTx(txBytes)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to issue transaction.")
		}
	}

	return nil
}

// waitForXChainTransactionAcceptance gets the status of [txID] and keeps querying until it
// has been accepted
func (runner RPCWorkFlowRunner) waitForXchainTransactionAcceptance(txID ids.ID) error {
	client := runner.client.XChainAPI()

	pollStartTime := time.Now()
	for time.Since(pollStartTime) < runner.networkAcceptanceTimeout {
		status, err := client.GetTxStatus(txID)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to get status.")
		}
		logrus.Tracef("Status for transaction %s: %s", txID, status)
		if status == choices.Accepted {
			return nil
		}
		if status == choices.Rejected {
			return stacktrace.NewError("Transaciton %s was rejected", txID)
		}
		time.Sleep(time.Second)
	}

	return stacktrace.NewError("Timed out waiting for transaction %s to be accepted on the XChain.", txID)
}

// AwaitXChainTxs confirms each transaction and returns an error if any of them are not confirmed
func (runner RPCWorkFlowRunner) AwaitXChainTxs(txIDs ...ids.ID) error {
	for _, txID := range txIDs {
		if err := runner.waitForXchainTransactionAcceptance(txID); err != nil {
			return err
		}
	}

	return nil
}

// AwaitPChainTxs confirms each transaction and returns an error if any of them are not confirmed
func (runner RPCWorkFlowRunner) AwaitPChainTxs(txIDs ...ids.ID) error {
	for _, txID := range txIDs {
		if err := runner.waitForPChainTransactionAcceptance(txID); err != nil {
			return err
		}
	}

	return nil
}

// waitForPChainTransactionAcceptance gets the status of [txID] and keeps querying until it
// has been accepted
func (runner RPCWorkFlowRunner) waitForPChainTransactionAcceptance(txID ids.ID) error {
	client := runner.client.PChainAPI()
	pollStartTime := time.Now()

	for time.Since(pollStartTime) < runner.networkAcceptanceTimeout {
		status, err := client.GetTxStatus(txID)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to get status")
		}
		logrus.Tracef("Status for transaction: %s: %s", txID, status)

		if status == platformvm.Committed {
			return nil
		}

		if status == platformvm.Dropped || status == platformvm.Aborted {
			return stacktrace.NewError("Abandoned Tx: %s because it had status: %s", txID, status)
		}
		time.Sleep(time.Second)
	}

	return stacktrace.NewError("Timed out waiting for transaction %s to be accepted on the PChain.", txID)
}

// VerifyPChainBalance verifies that the balance of P Chain Address: [address] is [expectedBalance]
func (runner RPCWorkFlowRunner) VerifyPChainBalance(address string, expectedBalance uint64) error {
	client := runner.client.PChainAPI()
	balance, err := client.GetBalance(address)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to retrieve P Chain balance.")
	}
	actualBalance := uint64(balance.Balance)
	if actualBalance != expectedBalance {
		return stacktrace.NewError("Found unexpected P Chain Balance for address: %s. Expected: %v, found: %v", address, expectedBalance, actualBalance)
	}

	return nil
}

// VerifyXChainAVABalance verifies that the balance of X Chain Address: [address] is [expectedBalance]
func (runner RPCWorkFlowRunner) VerifyXChainAVABalance(address string, expectedBalance uint64) error {
	client := runner.client.XChainAPI()
	balance, err := client.GetBalance(address, AvaxAssetID)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to retrieve X Chain balance.")
	}
	actualBalance := uint64(balance.Balance)
	if actualBalance != expectedBalance {
		return stacktrace.NewError("Found unexpected X Chain Balance for address: %s. Expected: %v, found: %v", address, expectedBalance, actualBalance)
	}

	return nil
}
