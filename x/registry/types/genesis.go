package types

import "fmt"

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:   DefaultParams(),
		NodeList: []Node{}}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
//
// It cannot check that the registry module account holds exactly the sum of
// all node bonds, because balances live in the bank module's genesis. Whoever
// assembles a genesis file with nodes in it must make those two agree.
func (gs GenesisState) Validate() error {
	nodeIdMap := make(map[uint64]bool)
	pubkeys := make(map[string]bool)
	nodeCount := gs.GetNodeCount()
	for _, elem := range gs.NodeList {
		if _, ok := nodeIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for node")
		}
		if elem.Id >= nodeCount {
			return fmt.Errorf("node id should be lower or equal than the last id")
		}
		nodeIdMap[elem.Id] = true

		if err := elem.ValidateBasic(); err != nil {
			return fmt.Errorf("node %d: %w", elem.Id, err)
		}
		if pubkeys[elem.Pubkey] {
			return fmt.Errorf("node %d: duplicated public key", elem.Id)
		}
		pubkeys[elem.Pubkey] = true
	}

	return gs.Params.Validate()
}
