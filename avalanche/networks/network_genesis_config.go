package networks

// NetworkGenesisConfig encapusulates genesis information describing
// a network
type NetworkGenesisConfig struct {
	Stakers         []StakerIdentity
	FundedAddresses FundedAddress
}

// FundedAddress encapsulates a pre-funded address
type FundedAddress struct {
	Address    string
	PrivateKey string
}

// StakerIdentity contains a staker's identifying information
type StakerIdentity struct {
	NodeID     string
	PrivateKey string
	TLSCert    string
}
