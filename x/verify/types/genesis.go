package types

import "fmt"

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:     DefaultParams(),
		ProberList: []Prober{}}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
	proberIdMap := make(map[uint64]bool)
	proberCount := gs.GetProberCount()
	for _, elem := range gs.ProberList {
		if _, ok := proberIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for prober")
		}
		if elem.Id >= proberCount {
			return fmt.Errorf("prober id should be lower or equal than the last id")
		}
		proberIdMap[elem.Id] = true
	}

	return gs.Params.Validate()
}
