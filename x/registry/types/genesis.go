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
func (gs GenesisState) Validate() error {
	nodeIdMap := make(map[uint64]bool)
	nodeCount := gs.GetNodeCount()
	for _, elem := range gs.NodeList {
		if _, ok := nodeIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for node")
		}
		if elem.Id >= nodeCount {
			return fmt.Errorf("node id should be lower or equal than the last id")
		}
		nodeIdMap[elem.Id] = true
	}

	return gs.Params.Validate()
}
