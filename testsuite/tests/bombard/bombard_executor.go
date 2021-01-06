package bombard

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-testing/avalanche/services"
	"github.com/ava-labs/avalanche-testing/testsuite/helpers"
	"github.com/ava-labs/avalanche-testing/testsuite/tester"
	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

// NewBombardExecutor returns a new bombard test bombardExecutor
func NewBombardExecutor(clients []*services.Client, numTxs, txFee uint64, acceptanceTimeout time.Duration) tester.AvalancheTester {
	return &bombardExecutor{
		normalClients:     clients,
		numTxs:            numTxs,
		acceptanceTimeout: acceptanceTimeout,
		txFee:             txFee,
	}
}

type bombardExecutor struct {
	normalClients     []*services.Client
	acceptanceTimeout time.Duration
	numTxs            uint64
	txFee             uint64
}

func createRandomString() string {
	return fmt.Sprintf("rand:%d", rand.Int())
}

// ExecuteTest implements the AvalancheTester interface
func (e *bombardExecutor) ExecuteTest() error {
	genesisClient := e.normalClients[0]
	secondaryClients := make([]*helpers.RPCWorkFlowRunner, len(e.normalClients)-1)
	xChainAddrs := make([]string, len(e.normalClients)-1)
	for i, client := range e.normalClients[1:] {
		secondaryClients[i] = helpers.NewRPCWorkFlowRunner(
			client,
			api.UserPass{Username: createRandomString(), Password: createRandomString()},
			e.acceptanceTimeout,
		)
		xChainAddress, _, err := secondaryClients[i].CreateDefaultAddresses()
		if err != nil {
			return stacktrace.Propagate(err, "Failed to create default addresses for client: %d", i)
		}
		xChainAddrs[i] = xChainAddress
	}

	genesisUser := api.UserPass{Username: createRandomString(), Password: createRandomString()}
	highLevelGenesisClient := helpers.NewRPCWorkFlowRunner(
		genesisClient,
		genesisUser,
		e.acceptanceTimeout,
	)

	if _, err := highLevelGenesisClient.ImportGenesisFunds(); err != nil {
		return stacktrace.Propagate(err, "Failed to fund genesis client.")
	}
	addrs, err := genesisClient.XChainAPI().ListAddresses(genesisUser)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to get genesis client's addresses")
	}
	if len(addrs) != 1 {
		return stacktrace.NewError("Found unexecpted number of addresses for genesis client: %d", len(addrs))
	}
	genesisAddress := addrs[0]
	logrus.Infof("Imported genesis funds at address: %s", genesisAddress)

	// Fund X Chain Addresses enough to issue [numTxs]
	seedAmount := (e.numTxs + 1) * e.txFee
	if err := highLevelGenesisClient.FundXChainAddresses(xChainAddrs, seedAmount); err != nil {
		return stacktrace.Propagate(err, "Failed to fund X Chain Addresses for Clients")
	}
	logrus.Infof("Funded X Chain Addresses with seedAmount %v.", seedAmount)

	codec, err := createXChainCodec()
	if err != nil {
		return stacktrace.Propagate(err, "Failed to initialize codec.")
	}
	utxoLists := make([][]*avax.UTXO, len(secondaryClients))
	for i, client := range secondaryClients {
		// Each address should have [e.txFee] remaining after sending [numTxs] and paying the fixed fee each time
		if err := client.VerifyXChainAVABalance(xChainAddrs[i], seedAmount); err != nil {
			return stacktrace.Propagate(err, "Failed to verify X Chain Balance for Client: %d", i)
		}
		utxosBytes, _, err := genesisClient.XChainAPI().GetUTXOs([]string{xChainAddrs[i]}, 10, "", "")
		if err != nil {
			return err
		}
		utxos := make([]*avax.UTXO, len(utxosBytes))
		for i, utxoBytes := range utxosBytes {
			utxo := &avax.UTXO{}
			_, err := codec.Unmarshal(utxoBytes, utxo)
			if err != nil {
				return stacktrace.Propagate(err, "Failed to unmarshal utxo bytes.")
			}
			utxos[i] = utxo
		}
		utxoLists[i] = utxos
		logrus.Infof("Decoded %d UTXOs", len(utxos))
	}
	logrus.Infof("Verified X Chain Balances and retrieved UTXOs.")

	// Create a string of consecutive transactions for each secondary client to send
	privateKeys := make([]*crypto.PrivateKeySECP256K1R, len(secondaryClients))
	txLists := make([][][]byte, len(secondaryClients))
	txIDLists := make([][]ids.ID, len(secondaryClients))
	for i, client := range e.normalClients[1:] {
		utxo := utxoLists[i][0]
		pkStr, err := client.XChainAPI().ExportKey(secondaryClients[i].User(), xChainAddrs[i])
		if err != nil {
			return stacktrace.Propagate(err, "Failed to export key.")
		}

		if !strings.HasPrefix(pkStr, constants.SecretKeyPrefix) {
			return fmt.Errorf("private key missing %s prefix", constants.SecretKeyPrefix)
		}
		trimmedPrivateKey := strings.TrimPrefix(pkStr, constants.SecretKeyPrefix)
		pkBytes, err := formatting.Decode(formatting.CB58, trimmedPrivateKey)
		if err != nil {
			return fmt.Errorf("problem parsing private key: %w", err)
		}

		factory := crypto.FactorySECP256K1R{}
		skIntf, err := factory.ToPrivateKey(pkBytes)
		sk := skIntf.(*crypto.PrivateKeySECP256K1R)
		privateKeys[i] = sk

		logrus.Infof("Creating string of %d transactions", e.numTxs)
		txs, txIDs, err := CreateConsecutiveTransactions(utxo, e.numTxs, seedAmount, e.txFee, sk)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to create transaction list.")
		}
		txLists[i] = txs
		txIDLists[i] = txIDs
	}

	wg := sync.WaitGroup{}
	issueTxsAsync := func(runner *helpers.RPCWorkFlowRunner, txList [][]byte) {
		if err := runner.IssueTxList(txList); err != nil {
			panic(err)
		}
		wg.Done()
	}

	startTime := time.Now()
	logrus.Infof("Beginning to issue transactions...")
	for i, client := range secondaryClients {
		wg.Add(1)
		issueTxsAsync(client, txLists[i])
	}
	wg.Wait()

	duration := time.Since(startTime)
	logrus.Infof("Finished issuing transaction lists in %v seconds.", duration.Seconds())
	for _, txIDs := range txIDLists {
		if err := highLevelGenesisClient.AwaitXChainTxs(txIDs...); err != nil {
			stacktrace.Propagate(err, "Failed to confirm transactions.")
		}
	}

	logrus.Infof("Confirmed all issued transactions.")

	return nil
}
