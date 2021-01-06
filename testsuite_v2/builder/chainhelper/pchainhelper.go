package chainhelper

import (
	"time"

	"github.com/ava-labs/avalanchego/vms/platformvm"

	"github.com/ava-labs/avalanche-testing/avalanche/services"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

// This helper automates some the most used functions in the PChain
type pChain struct {
}

// AwaitTransactionAcceptance waits for the [txID] to be committed within [timeout]
func (p *pChain) AwaitTransactionAcceptance(client *services.Client, txID ids.ID, timeout time.Duration) error {

	for startTime := time.Now(); time.Since(startTime) < timeout; time.Sleep(time.Second) {
		status, err := client.PChainAPI().GetTxStatus(txID, true)
		if err != nil {
			return stacktrace.Propagate(err, "Failed to get status")
		}
		logrus.Tracef("Status for transaction: %s: %s", txID, status.Status)

		if status.Status == platformvm.Committed {
			return nil
		}

		if status.Status == platformvm.Dropped || status.Status == platformvm.Aborted {
			return stacktrace.NewError("Abandoned Tx: %s because it had status: %s. Reason: %s", txID, status.Status, status.Reason)
		}
	}
	return stacktrace.NewError("Timed out waiting for transaction %s to be accepted on the PChain.", txID)
}

// CheckBalance validates the [address] balance is equal to [amount]
func (p *pChain) CheckBalance(client *services.Client, address string, amount uint64) error {

	pBalance, err := client.PChainAPI().GetBalance(address)
	if err != nil {
		return stacktrace.Propagate(err, "Failed to retrieve P Chain balance.")
	}
	pActualBalance := uint64(pBalance.Balance)
	if pActualBalance != amount {
		return stacktrace.NewError("Found unexpected P Chain Balance for address: %s. Expected: %v, found: %v",
			address, amount, pActualBalance)
	}

	return nil
}

// PChain is a helper to chain request to the correct VM
func PChain() *pChain {

	return &pChain{}
}
